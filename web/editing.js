// Elm owns edit decisions. This bridge owns cancellable audio playback only.
window.initializeEditingAudio = function (app) {
  let context;
  let source;
  let request;
  let generation = 0;
  const status = (message) => app.ports.editingAudioStatus.send(message);

  function cancelPreview() {
    generation += 1;
    request?.abort();
    request = null;
    if (source) {
      source.onended = null;
      source.stop();
      source = null;
    }
  }

  function stop() {
    cancelPreview();
    document.getElementById("editing-source")?.pause();
  }

  app.ports.editingAudio.subscribe(async (command) => {
    stop();
    const current = generation;
    if (command.action === "stop") return;
    try {
      if (command.action === "preview") {
        status("Loading preview…");
        context ??= new AudioContext();
        await context.resume();
        if (current !== generation) return;
        request = new AbortController();
        const response = await fetch(command.url, { signal: request.signal });
        if (!response.ok) throw new Error("Preview unavailable");
        const buffer = await context.decodeAudioData(await response.arrayBuffer());
        if (current !== generation) return;
        source = context.createBufferSource();
        source.buffer = buffer;
        source.connect(context.destination);
        source.onended = () => {
          source = null;
          status("Preview finished");
        };
        source.start();
        status("Playing preview");
      } else if (command.action === "full") {
        const audio = document.getElementById("editing-source");
        if (!audio) return;
        if (audio.readyState === 0) {
          await new Promise((resolve, reject) => {
            request = new AbortController();
            audio.addEventListener("loadedmetadata", resolve, { once: true, signal: request.signal });
            audio.addEventListener("error", reject, { once: true, signal: request.signal });
            request.signal.addEventListener("abort", reject, { once: true });
            audio.load();
          });
        }
        if (current !== generation) return;
        audio.currentTime = command.start;
        await audio.play();
        if (current === generation) status("Playing full section");
      }
    } catch (error) {
      if (current === generation && error?.name !== "AbortError") {
        status("Could not play audio. Try listening again.");
      }
    }
  });

  // Native controls remain available, but playback stays inside this section.
  document.addEventListener("loadedmetadata", (event) => {
    const audio = event.target;
    if (audio.id === "editing-source") audio.currentTime = Number(audio.dataset.start);
  }, true);
  document.addEventListener("play", (event) => {
    const audio = event.target;
    if (audio.id !== "editing-source") return;
    cancelPreview();
    const start = Number(audio.dataset.start);
    const end = Number(audio.dataset.end);
    if (audio.currentTime < start - 0.001 || audio.currentTime >= end) audio.currentTime = start;
    status("Playing full section");
  }, true);
  document.addEventListener("timeupdate", (event) => {
    const audio = event.target;
    if (audio.id !== "editing-source") return;
    const start = Number(audio.dataset.start);
    const end = Number(audio.dataset.end);
    // Chromium can report a FLAC seek a microsecond short of the target.
    // An exact comparison would keep seeking forever instead of playing.
    if (audio.currentTime < start - 0.001) audio.currentTime = start;
    if (audio.currentTime >= end && !audio.paused) {
      audio.pause();
      status("Section finished");
    }
  }, true);
};
