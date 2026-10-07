const { test } = require("node:test");
const assert = require("node:assert/strict");
const { readFileSync } = require("node:fs");
const { runInNewContext } = require("node:vm");

function setup() {
  const listeners = {}, statuses = [], sources = [];
  let command;
  const audio = {
    id: "editing-source", dataset: { start: "12", end: "19" },
    readyState: 1, currentTime: 0, paused: true,
    pause() { this.paused = true; },
    async play() { this.paused = false; listeners.play({ target: this }); }
  };
  class Audio {
    constructor(url) {
      this.src = url;
      this.paused = true;
      sources.push(this);
    }
    play() {
      this.paused = false;
      return new Promise((resolve, reject) => { this.resolve = resolve; this.reject = reject; });
    }
    pause() { this.paused = true; }
    removeAttribute(name) { if (name === "src") this.src = ""; }
    load() { this.released = true; }
  }
  const window = {};
  runInNewContext(readFileSync(__dirname + "/editing.js", "utf8"), {
    window, Audio, AbortController,
    document: { getElementById: () => audio, addEventListener: (event, fn) => { listeners[event] = fn; } }
  });
  window.initializeEditingAudio({ ports: {
    editingAudio: { subscribe: fn => { command = fn; } },
    editingAudioStatus: { send: value => statuses.push(value) }
  } });
  return { command, audio, listeners, statuses, sources };
}

test("a superseded audition cannot play or replace the latest status", async () => {
  const s = setup();
  const old = s.command({ action: "preview", url: "first" });
  const ended = s.sources[0].onended;
  const latest = s.command({ action: "preview", url: "second" });
  assert.equal(s.sources[0].paused, true);
  assert.equal(s.sources[0].src, "");
  assert.equal(s.sources[0].released, true);
  assert.equal(s.sources[1].src, "second");
  s.sources[1].resolve();
  await latest;
  s.sources[0].resolve();
  await old;
  ended();
  assert.equal(s.sources[1].paused, false);
  assert.equal(s.statuses.at(-1), "Playing preview");
  await s.command({ action: "stop" });
  assert.equal(s.sources[1].paused, true);
  assert.equal(s.sources[1].released, true);
  assert.equal(s.sources[1].onended, null);
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

test("native playback cancels an audition still loading", async () => {
  const s = setup();
  const pending = s.command({ action: "preview", url: "pending" });
  await s.audio.play();
  s.sources[0].reject(new Error("Interrupted playback"));
  await pending;
  assert.equal(s.sources[0].paused, true);
  assert.equal(s.sources[0].released, true);
  assert.equal(s.audio.currentTime, 12);
  assert.equal(s.statuses.at(-1), "Playing full section");
});

test("preview completion and playback failures report their actual outcome", async () => {
  const s = setup();
  const playing = s.command({ action: "preview", url: "clip" });
  s.sources[0].resolve();
  await playing;
  s.sources[0].onended();
  assert.equal(s.statuses.at(-1), "Preview finished");
  const failed = s.command({ action: "preview", url: "unavailable" });
  s.sources[1].reject(new Error("Unavailable"));
  await failed;
  assert.equal(s.statuses.at(-1), "Could not play audio. Try listening again.");
});
