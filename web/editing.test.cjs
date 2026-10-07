const { test } = require("node:test");
const assert = require("node:assert/strict");
const { readFileSync } = require("node:fs");
const { runInNewContext } = require("node:vm");

function setup() {
  const listeners = {}, statuses = [], requests = [], sources = [];
  let command;
  const audio = {
    id: "editing-source", dataset: { start: "12", end: "19" },
    readyState: 1, currentTime: 0, paused: true,
    pause() { this.paused = true; },
    async play() { this.paused = false; listeners.play({ target: this }); }
  };
  class AudioContext {
    async resume() {}
    async decodeAudioData(buffer) { return buffer; }
    createBufferSource() {
      const source = { started: false, stopped: false, connect() {}, start() { this.started = true; }, stop() { this.stopped = true; } };
      sources.push(source);
      return source;
    }
  }
  const window = {};
  runInNewContext(readFileSync(__dirname + "/editing.js", "utf8"), {
    window, AudioContext, AbortController,
    document: { getElementById: () => audio, addEventListener: (event, fn) => { listeners[event] = fn; } },
    fetch: (url, options) => new Promise(resolve => requests.push({ url, options, resolve }))
  });
  window.initializeEditingAudio({ ports: {
    editingAudio: { subscribe: fn => { command = fn; } },
    editingAudioStatus: { send: value => statuses.push(value) }
  } });
  return { command, audio, listeners, statuses, requests, sources };
}

const tick = () => new Promise(resolve => setImmediate(resolve));
const response = { ok: true, arrayBuffer: async () => new ArrayBuffer(8) };

test("a superseded audition cannot play or replace the latest status", async () => {
  const s = setup();
  const old = s.command({ action: "preview", url: "first" });
  await tick();
  const latest = s.command({ action: "preview", url: "second" });
  await tick();
  assert.equal(s.requests[0].options.signal.aborted, true);
  s.requests[1].resolve(response);
  await latest;
  s.requests[0].resolve(response);
  await old;
  assert.equal(s.sources.length, 1);
  assert.equal(s.sources[0].started, true);
  assert.equal(s.statuses.at(-1), "Playing preview");
  await s.command({ action: "stop" });
  assert.equal(s.sources[0].stopped, true);
  assert.equal(s.sources[0].onended, null);
});

test("full-section playback seeks to its start and stops at its finish", async () => {
  const s = setup();
  await s.command({ action: "full", start: 12, end: 19 });
  assert.equal(s.audio.currentTime, 12);
  assert.equal(s.audio.paused, false);
  s.audio.currentTime = 11.999999;
  s.listeners.timeupdate({ target: s.audio });
  assert.equal(s.audio.currentTime, 11.999999, "sub-millisecond rounding must not cause a seek loop");
  s.audio.currentTime = 11.8;
  s.listeners.timeupdate({ target: s.audio });
  assert.equal(s.audio.currentTime, 12, "seeks outside the section must still be clamped");
  s.audio.currentTime = 19.1;
  s.listeners.timeupdate({ target: s.audio });
  assert.equal(s.audio.paused, true);
  assert.equal(s.statuses.at(-1), "Section finished");
});

test("native playback cancels an audition still being decoded", async () => {
  const s = setup();
  const pending = s.command({ action: "preview", url: "pending" });
  await tick();
  await s.audio.play();
  s.requests[0].resolve(response);
  await pending;
  assert.equal(s.sources.length, 0);
  assert.equal(s.audio.currentTime, 12);
  assert.equal(s.statuses.at(-1), "Playing full section");
});
