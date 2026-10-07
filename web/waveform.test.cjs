const { test } = require("node:test");
const assert = require("node:assert/strict");
const { readFileSync } = require("node:fs");
const { runInNewContext } = require("node:vm");

function setup() {
  let Waveform;
  class HTMLElement {
    attrs = {};
    events = [];
    getAttribute(name) { return this.attrs[name] ?? null; }
    setAttribute(name, value) { this.attrs[name] = value; }
    dispatchEvent(event) { this.events.push(event); }
    append() {}
  }
  runInNewContext(readFileSync(__dirname + "/waveform.js", "utf8"), {
    HTMLElement,
    ResizeObserver: class { observe() {} },
    document: { createElement: () => ({
      listeners: {}, setAttribute() {},
      addEventListener(name, handler) { this.listeners[name] = handler; },
      getBoundingClientRect() { return { left: 10, width: 1000 }; }
    }) },
    CustomEvent: class { constructor(type, options) { this.type = type; this.detail = options.detail; } },
    customElements: { define: (_, cls) => { Waveform = cls; } }
  });
  const wave = new Waveform();
  wave.setAttribute("data-draft", JSON.stringify({
    duration: 100,
    breakpoints: [{ time: 0 }, { time: 10 }, { time: 11 }, { time: 63 }, { time: 100 }],
    sections: [{ keep: true }, { keep: false }, { keep: true }, { keep: false }]
  }));
  return wave;
}

function selection(wave, x, width = 1000) {
  return JSON.parse(JSON.stringify(wave.selectionAt(x, width)));
}

test("waveform maps asymmetric sections and clamped recording endpoints", () => {
  const wave = setup();
  assert.deepEqual(selection(wave, 50), { kind: "section", index: 0 });
  assert.deepEqual(selection(wave, 320), { kind: "section", index: 2 });
  assert.deepEqual(selection(wave, 900), { kind: "section", index: 3 });
  assert.deepEqual(selection(wave, -40), { kind: "section", index: 0 });
  assert.deepEqual(selection(wave, 1100), { kind: "section", index: 3 });
  assert.equal(wave.selectionAt(40, 0), null);
});

test("nearest breakpoint wins inside the pixel hit area at different widths", () => {
  const wave = setup();
  assert.deepEqual(selection(wave, 101), { kind: "boundary", index: 1 });
  assert.deepEqual(selection(wave, 109), { kind: "boundary", index: 2 });
  assert.deepEqual(selection(wave, 621), { kind: "section", index: 2 });
  assert.deepEqual(selection(wave, 623), { kind: "boundary", index: 3 });
  assert.deepEqual(selection(wave, 315, 500), { kind: "boundary", index: 3 });
});

test("selection only emits navigation and is disabled during mutations", () => {
  const wave = setup();
  wave.select(wave.selectionAt(400, 1000));
  assert.equal(wave.events.length, 1);
  assert.equal(wave.events[0].type, "waveformselect");
  assert.equal(wave.events[0].detail.index, 2);
  wave.setAttribute("data-disabled", "true");
  wave.select(wave.selectionAt(100, 1000));
  assert.equal(wave.events.length, 1);
});

test("zoom keeps the time under an off-center mouse pointer fixed", () => {
  const wave = setup();
  wave.zoom(2, 0.25);
  assert.equal(wave.view.start, 12.5);
  assert.equal(wave.view.span, 50);
  assert.equal(wave.view.start + wave.view.span * 0.25, 25);
  wave.zoom(2, 0.8);
  assert.equal(wave.view.start, 32.5);
  assert.equal(wave.view.span, 25);
  assert.equal(wave.view.start + wave.view.span * 0.8, 52.5);
  wave.zoom(0.5, 0.8);
  assert.equal(wave.view.start, 12.5);
  assert.equal(wave.view.span, 50);
});

test("pan and zoom clamp at both recording edges and reset to the full view", () => {
  const wave = setup();
  wave.zoom(4, 0.5);
  wave.pan(100);
  assert.equal(wave.view.start, 75);
  assert.equal(wave.view.span, 25);
  wave.pan(-100);
  assert.equal(wave.view.start, 0);
  wave.zoom(10000, 0);
  assert.equal(wave.view.span, 0.5);
  wave.setView(0, 100);
  assert.equal(wave.view.start, 0);
  assert.equal(wave.view.span, 100);
  assert.equal(wave.events.length, 0, "navigation must not select or mutate edits");
});

test("hit testing uses the zoomed and panned window, including nearby boundaries", () => {
  const wave = setup();
  wave.setView(7, 20);
  assert.deepEqual(selection(wave, 150), { kind: "boundary", index: 1 });
  assert.deepEqual(selection(wave, 200), { kind: "boundary", index: 2 });
  assert.deepEqual(selection(wave, 175), { kind: "section", index: 1 });
  assert.deepEqual(selection(wave, 170), { kind: "section", index: 1 });
  assert.deepEqual(selection(wave, 157), { kind: "boundary", index: 1 });
  wave.pan(40);
  assert.deepEqual(selection(wave, 800), { kind: "boundary", index: 3 });
  assert.deepEqual(selection(wave, 700), { kind: "section", index: 2 });
});

test("wheel zoom is quicker while horizontal and shift-wheel panning are gentler", () => {
  const wave = setup();
  wave.load = () => {};
  wave.connectedCallback();
  let prevented = 0;
  const wheel = values => wave.canvas.listeners.wheel({
    deltaMode: 0, deltaX: 0, deltaY: 0, clientX: 260, shiftKey: false,
    preventDefault() { prevented++; }, ...values
  });
  wheel({ deltaY: -100 });
  assert.ok(Math.abs(wave.view.span - 100 * Math.exp(-0.5)) < 1e-9);
  assert.ok(Math.abs(wave.view.start + 0.25 * wave.view.span - 25) < 1e-9);
  wave.setView(20, 50);
  wheel({ deltaX: 100 });
  assert.equal(wave.view.start, 21.75);
  wheel({ deltaY: -200, shiftKey: true });
  assert.equal(wave.view.start, 18.25);
  assert.equal(wave.view.span, 50);
  assert.equal(prevented, 3);
  assert.equal(wave.events.length, 0);
});

test("section body clicks beat adjacent markers and widen tiny section targets", () => {
  const wave = setup();
  const body = x => JSON.parse(JSON.stringify(wave.selectionAt(x, 1000, "section")));
  assert.deepEqual(body(105), { kind: "section", index: 1 });
  assert.deepEqual(body(98), { kind: "section", index: 1 });
  assert.equal(wave.selectionAt(98, 1000, "boundary").index, 1);
  assert.equal(wave.selectionAt(700, 1000, "boundary"), null);
  wave.setView(10, 0.5);
  assert.equal(wave.view.span, 0.5);
  assert.deepEqual(body(500), { kind: "section", index: 1 });
});

test("navigation reveals offscreen selections without moving a visible section", () => {
  const wave = setup();
  wave.setView(8, 0.5);
  wave.setAttribute("data-selection", "boundary:3");
  wave.revealSelection();
  assert.equal(wave.view.start, 62.75);
  wave.setAttribute("data-selection", "section:2");
  wave.revealSelection();
  assert.equal(wave.view.start, 62.75);
  wave.setAttribute("data-selection", "section:1");
  wave.revealSelection();
  assert.equal(wave.view.start, 10.25);
  assert.equal(wave.events.length, 0);
});
