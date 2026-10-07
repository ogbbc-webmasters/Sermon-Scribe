const { test } = require("node:test");
const assert = require("node:assert/strict");
const { readFileSync } = require("node:fs");
const { runInNewContext } = require("node:vm");

function setup(fetch = () => {}) {
  let Waveform;
  class HTMLElement {
    attrs = {};
    events = [];
    getAttribute(name) { return this.attrs[name] ?? null; }
    setAttribute(name, value) { this.attrs[name] = value; }
    removeAttribute(name) { delete this.attrs[name]; }
    dispatchEvent(event) { this.events.push(event); }
    append() {}
  }
  runInNewContext(readFileSync(__dirname + "/waveform.js", "utf8"), {
    HTMLElement, fetch, AbortController,
    window: { devicePixelRatio: 1 },
    ResizeObserver: class { observe() {} },
    document: { createElement: () => ({
      listeners: {}, attrs: {}, children: [],
      setAttribute(name, value) { this.attrs[name] = value; },
      append(...children) { this.children.push(...children); },
      remove() { this.removed = true; },
      clientWidth: 1000, focus() {}, setPointerCapture() {}, releasePointerCapture() {},
      classList: { add() {}, remove() {}, toggle() {} },
      addEventListener(name, handler) { this.listeners[name] = handler; },
      getBoundingClientRect() { return { left: 10, top: 20, width: 1000, height: 128 }; }
    }) },
    CustomEvent: class { constructor(type, options = {}) { this.type = type; this.detail = options.detail; } },
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
  assert.deepEqual(selection(wave, 607), { kind: "section", index: 2 });
  assert.deepEqual(selection(wave, 609), { kind: "boundary", index: 3 });
  assert.deepEqual(selection(wave, 623), { kind: "boundary", index: 3 });
  assert.deepEqual(selection(wave, 315, 500), { kind: "boundary", index: 3 });
});

test("left and right arrows traverse sections and breakpoints in time order", () => {
  const wave = setup();
  wave.load = () => {};
  wave.connectedCallback();
  wave.setAttribute("data-selection", "section:0");

  const press = key => {
    const eventCount = wave.events.length;
    let prevented = false;
    wave.canvas.listeners.keydown({ key, preventDefault() { prevented = true; } });
    const event = wave.events.length > eventCount ? wave.events.at(-1) : null;
    if (event) wave.setAttribute("data-selection", `${event.detail.kind}:${event.detail.index}`);
    return {
      selection: event && { kind: event.detail.kind, index: event.detail.index },
      keyboard: event?.detail.keyboard ?? false,
      prevented
    };
  };

  assert.deepEqual(press("ArrowLeft"), { selection: { kind: "section", index: 0 }, keyboard: true, prevented: true });
  assert.deepEqual(press("ArrowRight").selection, { kind: "boundary", index: 1 });
  assert.deepEqual(press("ArrowRight").selection, { kind: "section", index: 1 });
  assert.deepEqual(press("ArrowRight").selection, { kind: "boundary", index: 2 });
  assert.deepEqual(press("ArrowRight").selection, { kind: "section", index: 2 });
  assert.deepEqual(press("ArrowRight").selection, { kind: "boundary", index: 3 });
  assert.deepEqual(press("ArrowRight").selection, { kind: "section", index: 3 });
  assert.deepEqual(press("ArrowRight").selection, { kind: "section", index: 3 });
  assert.deepEqual(press("ArrowLeft").selection, { kind: "boundary", index: 3 });
  assert.deepEqual(press("ArrowLeft").selection, { kind: "section", index: 2 });

  assert.deepEqual(press("ArrowUp"), { selection: null, keyboard: false, prevented: false });
  assert.deepEqual(press("ArrowDown"), { selection: null, keyboard: false, prevented: false });
});

test("Delete and Backspace remove a selected interior breakpoint on the waveform", () => {
  for (const key of ["Delete", "Backspace"]) {
    const wave = setup();
    wave.load = () => {};
    wave.connectedCallback();
    wave.setAttribute("data-selection", "boundary:1");
    let prevented = false;

    wave.canvas.listeners.keydown({ key, preventDefault() { prevented = true; } });

    assert.equal(prevented, true);
    assert.equal(wave.events[0].type, "waveformdelete");
  }

  const endpoint = setup();
  endpoint.load = () => {};
  endpoint.connectedCallback();
  endpoint.setAttribute("data-selection", "boundary:0");
  endpoint.canvas.listeners.keydown({ key: "Delete", preventDefault() {} });
  assert.equal(endpoint.events.length, 0);
});

test("breakpoints are clickable along their lines and sections have a separate strip", () => {
  const wave = setup();
  wave.load = () => {};
  wave.connectedCallback();
  for (const x of [609, 651]) assert.equal(wave.selectionAt(x, 1000, "boundary").index, 3);
  for (const x of [607, 653]) assert.equal(wave.selectionAt(x, 1000, "boundary"), null);
  assert.equal(wave.selectionAt(104, 1000, "boundary").index, 1);
  assert.equal(wave.selectionAt(106, 1000, "boundary").index, 2);
  const tap = y => {
    const event = { button: 0, pointerId: 1, clientX: 661, clientY: y };
    wave.canvas.listeners.pointerdown(event);
    wave.canvas.listeners.pointerup(event);
  };
  tap(63);
  assert.equal(wave.events[0].detail.kind, "boundary");
  assert.equal(wave.events[0].detail.index, 3);
  assert.equal(wave.events[0].detail.keyboard, undefined);
  tap(65);
  assert.equal(wave.events[1].detail.kind, "boundary");
  assert.equal(wave.events[1].detail.index, 3);
  tap(103);
  assert.equal(wave.events[2].detail.kind, "boundary");
  tap(105);
  assert.equal(wave.events[3].detail.kind, "section");
  assert.equal(wave.events[3].detail.index, 3);
  const tiny = { button: 0, pointerId: 1, clientX: 108, clientY: 125 };
  wave.canvas.listeners.pointerdown(tiny);
  wave.canvas.listeners.pointerup(tiny);
  assert.equal(wave.events[4].detail.kind, "section");
  assert.equal(wave.events[4].detail.index, 1, "tiny section keeps its enlarged target in the strip");
});

test("two-finger zoom follows the midpoint and resumes panning without selecting", () => {
  const wave = setup();
  wave.load = () => {};
  wave.connectedCallback();
  wave.setView(20, 50);
  wave.setAttribute("data-placing", "true");
  const pointer = (pointerId, clientX) => ({ button: 0, pointerId, clientX, clientY: 70, pointerType: "touch" });
  wave.canvas.listeners.pointerdown(pointer(1, 210));
  wave.canvas.listeners.pointerdown(pointer(2, 510));
  wave.canvas.listeners.pointermove(pointer(1, 110));
  wave.canvas.listeners.pointermove(pointer(2, 610));
  assert.equal(wave.view.span, 30);
  assert.equal(wave.view.start, 27);
  wave.canvas.listeners.pointermove(pointer(1, 210));
  wave.canvas.listeners.pointermove(pointer(2, 710));
  assert.equal(wave.view.span, 30);
  assert.equal(wave.view.start, 24);
  wave.canvas.listeners.pointerup(pointer(2, 710));
  wave.canvas.listeners.pointermove(pointer(1, 310));
  assert.equal(wave.view.start, 21);
  wave.canvas.listeners.pointerup(pointer(1, 310));
  assert.equal(wave.events.length, 0);
  wave.setAttribute("data-placing", "false");
  wave.canvas.listeners.pointerdown(pointer(3, 200));
  wave.canvas.listeners.pointercancel(pointer(3, 200));
  assert.equal(wave.events.length, 0, "cancelled touches must not select");
  wave.canvas.listeners.pointerdown(pointer(4, 200));
  wave.canvas.listeners.pointerup(pointer(4, 200));
  assert.equal(wave.events.length, 1, "a later tap must still work");
});

test("touch pinch clamps zoom and lost capture does not turn into a tap", () => {
  const wave = setup();
  wave.load = () => {};
  wave.connectedCallback();
  wave.setView(20, 50);
  const pointer = (pointerId, clientX) => ({ button: 0, pointerId, clientX, clientY: 70 });
  wave.canvas.listeners.pointerdown(pointer(1, 210));
  wave.canvas.listeners.pointerdown(pointer(2, 510));
  wave.canvas.listeners.pointermove(pointer(2, 210.001));
  assert.equal(wave.view.span, 100);
  assert.equal(wave.view.start, 0);
  wave.canvas.listeners.pointermove(pointer(2, 100000));
  assert.equal(wave.view.span, 0.5);
  wave.canvas.listeners.lostpointercapture(pointer(1, 210));
  wave.canvas.listeners.pointerup(pointer(2, 100000));
  assert.equal(wave.events.length, 0);
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
  assert.deepEqual(selection(wave, 174), { kind: "section", index: 1 });
  assert.deepEqual(selection(wave, 170), { kind: "boundary", index: 1 });
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

test("manual placement uses the zoomed click time; dragging and disabled clicks do not add", () => {
  const wave = setup();
  wave.load = () => {};
  wave.connectedCallback();
  wave.setView(7, 20);
  wave.setAttribute("data-placing", "true");
  const pointer = x => ({ button: 0, pointerId: 1, clientX: x, clientY: 50 });
  wave.canvas.listeners.pointerdown(pointer(260));
  wave.canvas.listeners.pointerup(pointer(260));
  assert.equal(wave.events[0].type, "waveformadd");
  assert.equal(wave.events[0].detail.time, 12);
  wave.canvas.listeners.pointerdown(pointer(260));
  wave.canvas.listeners.pointermove(pointer(310));
  wave.canvas.listeners.pointerup(pointer(310));
  assert.equal(wave.events.length, 1);
  wave.setAttribute("data-disabled", "true");
  wave.addAt(600, 1000);
  assert.equal(wave.events.length, 1);
  wave.canvas.listeners.keydown({ key: "Escape", preventDefault() {} });
  assert.equal(wave.events[1].type, "waveformcancel");
});

test("manual breakpoints snap to nearby quiet audio", () => {
  const wave = setup();
  wave.setAttribute("data-draft", JSON.stringify({
    duration: 2,
    breakpoints: [{ time: 0 }, { time: 2 }],
    sections: [{ keep: true }]
  }));
  wave.hires = new Int16Array(200).fill(12000);
  for (let pair = 50; pair <= 55; pair++) {
    wave.hires[pair * 2] = 0;
    wave.hires[pair * 2 + 1] = 0;
  }

  wave.addAt(470, 1000);
  assert.ok(Math.abs(wave.events[0].detail.time - 1.03) < 1e-9);
});

test("quiet snapping stays at the requested position when there is no quieter nearby point", () => {
  const wave = setup();
  wave.setAttribute("data-draft", JSON.stringify({
    duration: 2,
    breakpoints: [{ time: 0 }, { time: 2 }],
    sections: [{ keep: true }]
  }));
  wave.hires = new Int16Array(200).fill(12000);

  assert.equal(wave.snapToQuiet(1), 1);
});

test("all zoom levels aggregate signed high-resolution peaks without an overview fallback", () => {
  const wave = setup();
  wave.hires = new Int16Array(10000);
  wave.hires.fill(0);
  wave.hires[1000] = -16384;
  wave.hires[1001] = 24576;

  const detail = wave.waveformRangeAt(10, 10.1);
  assert.equal(detail.min, -0.5);
  assert.equal(detail.max, 0.75);
  wave.hires[1040] = -28672;
  wave.hires[1041] = 8192;
  const overview = wave.waveformRangeAt(10, 12);
  assert.equal(overview.min, -0.875);
  assert.equal(overview.max, 0.75);
  const subBucket = wave.waveformRangeAt(10.001, 10.002);
  assert.equal(subBucket.min, -0.5);
  assert.equal(subBucket.max, 0.75);
});

test("only high-resolution data is requested and the spinner lasts until its body arrives", async () => {
  const requests = [];
  let finishBody;
  const wave = setup(url => new Promise(resolve => requests.push({ url, resolve })));
  wave.setAttribute("src", "/waveform");
  const draws = [];
  wave.draw = () => draws.push(wave.hires && Array.from(wave.hires));
  const pending = wave.load();
  assert.equal(wave.getAttribute("aria-busy"), "true");
  assert.equal(wave.status.attrs.role, "status");
  assert.equal(wave.status.children[0].attrs.icon, "ph:spinner-gap");
  assert.equal(wave.status.children[1], "Loading waveform…");
  assert.equal(requests[0].url, "/waveform/highres");
  requests[0].resolve({ ok: true, headers: { get: () => "50" },
    arrayBuffer: () => new Promise(resolve => { finishBody = resolve; }) });
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(wave.getAttribute("aria-busy"), "true");
  assert.equal(wave.status.removed, undefined);
  const buffer = new ArrayBuffer(4);
  const view = new DataView(buffer);
  view.setInt16(0, -16384, true);
  view.setInt16(2, 24576, true);
  finishBody(buffer);
  await pending;
  assert.equal(wave.status.removed, true);
  assert.equal(wave.getAttribute("aria-busy"), "false");
  assert.deepEqual(Array.from(wave.hires), [-16384, 24576]);
  assert.deepEqual(draws, [null, [-16384, 24576]]);
  assert.equal(requests.length, 1);
});

test("failed and superseded loads cannot leave a spinner or overwrite the new status", async () => {
  const requests = [];
  const wave = setup(() => new Promise((resolve, reject) => requests.push({ resolve, reject })));
  const old = wave.load();
  const previous = wave.status;
  const latest = wave.load();
  assert.equal(previous.removed, true);
  requests[0].reject(new Error("Old request failed"));
  await old;
  assert.equal(wave.getAttribute("aria-busy"), "true");
  assert.equal(wave.status.textContent, undefined);
  requests[1].resolve({ ok: false });
  await latest;
  assert.match(wave.status.textContent, /Waveform unavailable/);
  assert.equal(wave.getAttribute("aria-busy"), "false");
});

test("missing high-resolution data clears the canvas without drawing a false silence line", () => {
  const wave = setup();
  wave.clientWidth = 1000;
  wave.clientHeight = 128;
  wave.canvas = { getContext() { assert.fail("must not draw without high-resolution data"); } };
  wave.hires = null;
  wave.draw();
  assert.equal(wave.canvas.width, 1000);
  assert.equal(wave.canvas.height, 128);
});

test("a superseded high-resolution body cannot overwrite the latest data", async () => {
  let finishOld;
  const wave = setup(async () => ({ ok: true, headers: { get: () => "50" },
    arrayBuffer: () => new Promise(resolve => { finishOld = resolve; }) }));
  const old = wave.load();
  await new Promise(resolve => setImmediate(resolve));
  wave.request.abort();
  wave.hires = new Int16Array([-8192, 16384]);
  finishOld(new ArrayBuffer(4));
  await old;
  assert.deepEqual(Array.from(wave.hires), [-8192, 16384]);
});
