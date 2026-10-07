// Elm owns selections and edit decisions; this element draws their audio overview.
const waveformTargetSize = 44;

customElements.define("editing-waveform", class extends HTMLElement {
  static observedAttributes = ["src", "data-draft", "data-selection", "data-disabled", "data-placing"];

  connectedCallback() {
    this.canvas = document.createElement("canvas");
    this.canvas.id = "editing-waveform-canvas";
    this.canvas.className = "editor__waveform-canvas";
    this.canvas.tabIndex = 0;
    this.canvas.setAttribute("role", "button");
    this.canvas.setAttribute("aria-label", "Audio waveform. Scroll or pinch to zoom, drag to pan, double-click to show all audio. Left and right arrows select sections; up and down arrows select breakpoints.");
    this.canvas.title = "Scroll or pinch to zoom · Drag to pan · Double-click to reset";
    this.pointers = new Map();
    this.drag = null;
    this.pinch = null;
    this.append(this.canvas);
    this.canvas.addEventListener("wheel", event => {
      event.preventDefault();
      const bounds = this.canvas.getBoundingClientRect();
      const unit = event.deltaMode === 1 ? 16 : event.deltaMode === 2 ? bounds.width : 1;
      if (event.shiftKey || Math.abs(event.deltaX) > Math.abs(event.deltaY)) {
        this.pan((event.shiftKey ? event.deltaY : event.deltaX) * unit / bounds.width * this.view.span * 0.35);
      } else {
        this.zoom(Math.exp(-event.deltaY * unit * 0.005), (event.clientX - bounds.left) / bounds.width);
      }
    }, { passive: false });
    this.canvas.addEventListener("pointerdown", event => {
      if (event.button !== 0 || this.pointers.size >= 2) return;
      this.canvas.focus();
      this.canvas.setPointerCapture(event.pointerId);
      this.canvas.classList.remove("editor__waveform-canvas--selecting");
      this.pointers.set(event.pointerId, { x: event.clientX, y: event.clientY });
      if (this.pointers.size === 2) {
        const [a, b] = this.pointers.values();
        const bounds = this.canvas.getBoundingClientRect();
        this.pinch = {
          distance: Math.hypot(b.x - a.x, b.y - a.y),
          span: this.view.span,
          time: this.view.start + ((a.x + b.x) / 2 - bounds.left) / bounds.width * this.view.span
        };
        this.drag.moved = true;
        this.canvas.classList.add("editor__waveform-canvas--dragging");
      } else {
        this.drag = { x: event.clientX, y: event.clientY, start: this.view.start, moved: false };
      }
    });
    this.canvas.addEventListener("pointermove", event => {
      if (!this.pointers.has(event.pointerId)) {
        const bounds = this.canvas.getBoundingClientRect();
        const overSectionStrip = event.clientY - bounds.top >= bounds.height - waveformTargetSize;
        const overBreakpoint = this.selectionAt(event.clientX - bounds.left, bounds.width)?.kind === "boundary";
        this.canvas.classList.toggle("editor__waveform-canvas--selecting", this.getAttribute("data-placing") !== "true" && (overSectionStrip || overBreakpoint));
        return;
      }
      this.pointers.set(event.pointerId, { x: event.clientX, y: event.clientY });
      if (this.pinch) {
        const [a, b] = this.pointers.values();
        const distance = Math.hypot(b.x - a.x, b.y - a.y);
        if (!distance || !this.pinch.distance) return;
        const bounds = this.canvas.getBoundingClientRect();
        const span = Math.max(Math.min(this.draft.duration, 0.5), Math.min(this.draft.duration, this.pinch.span * this.pinch.distance / distance));
        this.setView(this.pinch.time - ((a.x + b.x) / 2 - bounds.left) / bounds.width * span, span);
        return;
      }
      const distance = event.clientX - this.drag.x;
      if (Math.hypot(distance, event.clientY - this.drag.y) > 4) this.drag.moved = true;
      if (this.drag.moved) {
        this.canvas.classList.add("editor__waveform-canvas--dragging");
        this.setView(this.drag.start - distance / this.canvas.clientWidth * this.view.span, this.view.span);
      }
    });
    this.canvas.addEventListener("pointerup", event => {
      if (!this.pointers.has(event.pointerId)) return;
      this.endPointer(event);
      this.canvas.releasePointerCapture(event.pointerId);
    });
    this.canvas.addEventListener("pointercancel", event => this.endPointer(event, true));
    this.canvas.addEventListener("lostpointercapture", event => this.endPointer(event, true));
    this.canvas.addEventListener("dblclick", () => {
      this.setView(0, this.draft.duration);
    });
    this.canvas.addEventListener("keydown", event => {
      const draft = this.draft;
      if (!draft) return;
      if (event.key === "Escape" && this.getAttribute("data-placing") === "true") {
        event.preventDefault();
        this.dispatchEvent(new CustomEvent("waveformcancel"));
        return;
      }
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

  endPointer(event, cancelled = false) {
    if (!this.pointers.delete(event.pointerId)) return;
    this.pinch = null;
    if (this.pointers.size) {
      const [remaining] = this.pointers.values();
      this.drag = { ...remaining, start: this.view.start, moved: true };
      return;
    }
    const moved = this.drag.moved;
    this.drag = null;
    this.canvas.classList.remove("editor__waveform-canvas--dragging");
    if (!cancelled && !moved) {
      const bounds = this.canvas.getBoundingClientRect();
      if (this.getAttribute("data-placing") === "true") this.addAt(event.clientX - bounds.left, bounds.width);
      else this.select(this.selectionAt(event.clientX - bounds.left, bounds.width, event.clientY - bounds.top >= bounds.height - waveformTargetSize ? "section" : "auto"));
    }
  }

  disconnectedCallback() {
    this.resize?.disconnect();
    this.request?.abort();
    this.canvas?.remove();
  }

  attributeChangedCallback(name) {
    if (!this.canvas) return;
    if (name === "src") this.load();
    else if (name === "data-placing") {
      this.canvas.classList.toggle("editor__waveform-canvas--placing", this.getAttribute("data-placing") === "true");
      this.canvas.classList.remove("editor__waveform-canvas--selecting");
    } else if (name === "data-selection") this.revealSelection();
    else this.draw();
  }

  revealSelection() {
    const [kind, value] = (this.getAttribute("data-selection") || "").split(":");
    const index = Number(value), draft = this.draft;
    const left = draft?.breakpoints[index]?.time;
    const right = kind === "section" ? draft?.breakpoints[index + 1]?.time : left;
    const { start, span } = this.view;
    if (left > start + span || right < start) this.setView((left + right) / 2 - span / 2, span);
    else this.draw();
  }

  get draft() {
    return JSON.parse(this.getAttribute("data-draft") || "null");
  }

  get view() {
    const duration = this.draft.duration;
    const span = Math.min(duration, this.viewSpan ?? duration);
    return { start: Math.max(0, Math.min(duration - span, this.viewStart ?? 0)), span };
  }

  setView(start, span) {
    const duration = this.draft.duration;
    this.viewSpan = Math.max(Math.min(duration, 0.5), Math.min(duration, span));
    this.viewStart = Math.max(0, Math.min(duration - this.viewSpan, start));
    this.draw();
  }

  zoom(factor, anchor) {
    const { start, span } = this.view;
    anchor = Math.max(0, Math.min(1, anchor));
    const next = Math.max(Math.min(this.draft.duration, 0.5), Math.min(this.draft.duration, span / factor));
    this.setView(start + anchor * (span - next), next);
  }

  pan(seconds) {
    this.setView(this.view.start + seconds, this.view.span);
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

  selectionAt(x, width, target = "auto") {
    const draft = this.draft;
    if (!draft || width <= 0) return null;
    const { start, span } = this.view;
    const time = start + Math.max(0, Math.min(1, x / width)) * span;
    let closest = null, distance = waveformTargetSize / 2;
    draft.breakpoints.forEach((boundary, index) => {
      if (index === 0 || index === draft.breakpoints.length - 1) return;
      if (boundary.time < start || boundary.time > start + span) return;
      const delta = Math.abs(boundary.time - time) / span * width;
      if (delta < distance) { closest = { kind: "boundary", index }; distance = delta; }
    });
    if (target !== "section" && closest) return closest;
    if (target === "boundary") return null;
    if (target === "section") {
      let nearby = null, nearest = 12;
      draft.sections.forEach((section, index) => {
        const left = draft.breakpoints[index].time, right = draft.breakpoints[index + 1].time;
        if (right < start || left > start + span || (right - left) / span * width >= 24) return;
        const delta = Math.abs(((left + right) / 2 - start) / span * width - x);
        if (delta < nearest) { nearby = { kind: "section", index }; nearest = delta; }
      });
      if (nearby) return nearby;
    }
    const index = draft.breakpoints.findIndex(boundary => boundary.time > time) - 1;
    return { kind: "section", index: index < 0 ? draft.sections.length - 1 : index };
  }

  addAt(x, width) {
    if (width <= 0 || this.getAttribute("data-disabled") === "true") return;
    const { start, span } = this.view;
    const time = start + Math.max(0, Math.min(1, x / width)) * span;
    this.dispatchEvent(new CustomEvent("waveformadd", { detail: { time } }));
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
    const { start: viewStart, span } = this.view;
    const position = time => (time - viewStart) / span * width;
    const selection = this.getAttribute("data-selection");
    const waveHeight = height - waveformTargetSize;
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
      const from = Math.floor((viewStart + x / width * span) / draft.duration * peaks.length);
      const to = Math.max(from + 1, Math.ceil((viewStart + (x + 1) / width * span) / draft.duration * peaks.length));
      for (let i = from; i < Math.min(to, peaks.length); i++) peak = Math.max(peak, peaks[i]);
      const amplitude = Math.max(0.5, peak * (waveHeight / 2 - 10));
      ctx.strokeStyle = color(draft.sections[section].keep ? "--green" : "--red");
      ctx.lineWidth = 1;
      ctx.beginPath();
      ctx.moveTo(x + 0.5, waveHeight / 2 - amplitude);
      ctx.lineTo(x + 0.5, waveHeight / 2 + amplitude);
      ctx.stroke();
    }
    draft.breakpoints.forEach((boundary, index) => {
      if (index === 0 || index === draft.breakpoints.length - 1) return;
      const x = position(boundary.time);
      ctx.strokeStyle = color(selection === `boundary:${index}` ? "--focus" : "--ink-soft");
      ctx.lineWidth = selection === `boundary:${index}` ? 3 : 1;
      ctx.beginPath();
      ctx.moveTo(x, 0);
      ctx.lineTo(x, waveHeight);
      ctx.stroke();
      ctx.fillStyle = ctx.strokeStyle;
      ctx.beginPath();
      ctx.moveTo(x - 4, 0);
      ctx.lineTo(x + 4, 0);
      ctx.lineTo(x, 7);
      ctx.fill();
    });
    ctx.strokeStyle = color("--line-strong");
    ctx.lineWidth = 1;
    ctx.beginPath();
    ctx.moveTo(0, waveHeight);
    ctx.lineTo(width, waveHeight);
    ctx.stroke();
    ctx.font = `600 ${color("--font-size-compact-action")} ${color("--font-body")}`;
    ctx.textAlign = "center";
    ctx.textBaseline = "middle";
    draft.sections.forEach((section, index) => {
      const start = Math.max(0, position(draft.breakpoints[index].time));
      const end = Math.min(width, position(draft.breakpoints[index + 1].time));
      if (end <= start) return;
      ctx.strokeStyle = color("--line-strong");
      ctx.beginPath();
      ctx.moveTo(end, waveHeight);
      ctx.lineTo(end, height);
      ctx.stroke();
      const caption = String(index + 1);
      if (end - start >= ctx.measureText(caption).width + parseFloat(color("--space-xs"))) {
        ctx.fillStyle = color(section.keep ? "--green" : "--red");
        ctx.fillText(caption, (start + end) / 2, waveHeight + waveformTargetSize / 2);
      }
    });
  }
});
