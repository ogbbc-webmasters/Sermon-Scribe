// Elm owns selections and edit decisions; this element draws their audio overview.
customElements.define("editing-waveform", class extends HTMLElement {
  static observedAttributes = ["src", "data-draft", "data-selection", "data-disabled"];

  connectedCallback() {
    this.canvas = document.createElement("canvas");
    this.canvas.className = "editor__waveform-canvas";
    this.canvas.tabIndex = 0;
    this.canvas.setAttribute("role", "button");
    this.canvas.setAttribute("aria-label", "Audio waveform. Click a section or breakpoint. Left and right arrows select sections; up and down arrows select breakpoints.");
    this.append(this.canvas);
    this.canvas.addEventListener("click", event => {
      const bounds = this.canvas.getBoundingClientRect();
      this.select(this.selectionAt(event.clientX - bounds.left, bounds.width));
    });
    this.canvas.addEventListener("keydown", event => {
      const draft = this.draft;
      if (!draft) return;
      const [kind, index] = (this.getAttribute("data-selection") || "section:0").split(":");
      if (event.key === "ArrowLeft" || event.key === "ArrowRight") {
        event.preventDefault();
        this.select({ kind: "section", index: Math.max(0, Math.min(draft.sections.length - 1, Number(index) + (event.key === "ArrowLeft" ? -1 : 1))) });
      } else if ((event.key === "ArrowUp" || event.key === "ArrowDown") && draft.breakpoints.length > 2) {
        event.preventDefault();
        this.select({ kind: "boundary", index: Math.max(1, Math.min(draft.breakpoints.length - 2, Number(index) + (event.key === "ArrowUp" ? -1 : 1))) });
      } else if (event.key === "Enter" || event.key === " ") {
        event.preventDefault();
        this.select({ kind, index: Number(index) });
      }
    });
    this.resize = new ResizeObserver(() => this.draw());
    this.resize.observe(this);
    this.load();
  }

  disconnectedCallback() {
    this.resize?.disconnect();
    this.request?.abort();
    this.canvas?.remove();
  }

  attributeChangedCallback(name) {
    if (!this.canvas) return;
    if (name === "src") this.load();
    else this.draw();
  }

  get draft() {
    return JSON.parse(this.getAttribute("data-draft") || "null");
  }

  async load() {
    this.request?.abort();
    const request = this.request = new AbortController();
    this.setAttribute("aria-busy", "true");
    this.peaks = [];
    this.draw();
    try {
      const response = await fetch(this.getAttribute("src"), { signal: request.signal });
      if (!response.ok) throw new Error("Waveform unavailable");
      const wave = await response.json();
      if (request.signal.aborted) return;
      this.peaks = wave.peaks;
      this.removeAttribute("title");
      this.draw();
    } catch (error) {
      if (error.name !== "AbortError") this.setAttribute("title", "Waveform unavailable. You can still select sections with the arrow keys.");
    } finally {
      if (this.request === request) this.setAttribute("aria-busy", "false");
    }
  }

  selectionAt(x, width) {
    const draft = this.draft;
    if (!draft || width <= 0) return null;
    const time = Math.max(0, Math.min(draft.duration, x / width * draft.duration));
    let closest = null, distance = 8;
    draft.breakpoints.forEach((boundary, index) => {
      if (index === 0 || index === draft.breakpoints.length - 1) return;
      const delta = Math.abs(boundary.time - time) / draft.duration * width;
      if (delta < distance) { closest = { kind: "boundary", index }; distance = delta; }
    });
    if (closest) return closest;
    const index = draft.breakpoints.findIndex(boundary => boundary.time > time) - 1;
    return { kind: "section", index: index < 0 ? draft.sections.length - 1 : index };
  }

  select(selection) {
    if (!selection || this.getAttribute("data-disabled") === "true") return;
    this.dispatchEvent(new CustomEvent("waveformselect", { detail: selection }));
  }

  draw() {
    const draft = this.draft;
    if (!draft || !this.canvas) return;
    const width = this.clientWidth, height = this.clientHeight;
    if (!width || !height) return;
    const ratio = window.devicePixelRatio || 1;
    this.canvas.width = Math.round(width * ratio);
    this.canvas.height = Math.round(height * ratio);
    const ctx = this.canvas.getContext("2d");
    ctx.scale(ratio, ratio);
    const styles = getComputedStyle(this);
    const color = name => styles.getPropertyValue(name).trim();
    const position = time => time / draft.duration * width;
    const selection = this.getAttribute("data-selection");
    draft.sections.forEach((section, index) => {
      const start = position(draft.breakpoints[index].time);
      const end = position(draft.breakpoints[index + 1].time);
      ctx.fillStyle = color(section.keep ? "--green-soft" : "--red-soft");
      ctx.fillRect(start, 0, end - start, height);
      if (selection === `section:${index}`) {
        ctx.strokeStyle = color("--focus");
        ctx.lineWidth = 3;
        ctx.strokeRect(start + 1.5, 1.5, Math.max(0, end - start - 3), height - 3);
      }
    });
    const peaks = this.peaks || [];
    let section = 0;
    for (let x = 0; x < width; x++) {
      while (section < draft.sections.length - 1 && x >= position(draft.breakpoints[section + 1].time)) section++;
      let peak = 0;
      const from = Math.floor(x / width * peaks.length);
      const to = Math.max(from + 1, Math.ceil((x + 1) / width * peaks.length));
      for (let i = from; i < Math.min(to, peaks.length); i++) peak = Math.max(peak, peaks[i]);
      const amplitude = Math.max(0.5, peak * (height / 2 - 10));
      ctx.strokeStyle = color(draft.sections[section].keep ? "--green" : "--red");
      ctx.lineWidth = 1;
      ctx.beginPath();
      ctx.moveTo(x + 0.5, height / 2 - amplitude);
      ctx.lineTo(x + 0.5, height / 2 + amplitude);
      ctx.stroke();
    }
    draft.breakpoints.forEach((boundary, index) => {
      if (index === 0 || index === draft.breakpoints.length - 1) return;
      const x = position(boundary.time);
      ctx.strokeStyle = color(selection === `boundary:${index}` ? "--focus" : "--ink-soft");
      ctx.lineWidth = selection === `boundary:${index}` ? 3 : 1;
      ctx.beginPath();
      ctx.moveTo(x, 0);
      ctx.lineTo(x, height);
      ctx.stroke();
      ctx.fillStyle = ctx.strokeStyle;
      ctx.beginPath();
      ctx.moveTo(x - 4, 0);
      ctx.lineTo(x + 4, 0);
      ctx.lineTo(x, 7);
      ctx.fill();
    });
  }
});
