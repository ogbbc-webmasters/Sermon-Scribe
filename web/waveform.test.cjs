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
  }
  runInNewContext(readFileSync(__dirname + "/waveform.js", "utf8"), {
    HTMLElement,
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
