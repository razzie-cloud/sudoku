(() => {
  'use strict';

  const noteAlphabet = '0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz-_';
  const digit = value => value || '';
  const encodeNotes = notes => notes.map(mask => noteAlphabet[mask >> 6] + noteAlphabet[mask & 63]).join('');
  const decodeNotes = text => Array.from({length: 81}, (_, i) =>
    (noteAlphabet.indexOf(text[2 * i]) << 6) | noteAlphabet.indexOf(text[2 * i + 1]));
  const leaveMessage = 'Leave this board and discard unsaved moves?';

  class Editor {
    constructor(root) {
      this.root = root;
      this.cells = [...root.querySelectorAll('[data-cell]')];
      this.controls = root.querySelector('[data-controls]');
      this.status = root.querySelector('[data-status]');
      this.actions = root.querySelector('[data-save-actions]');
      this.retry = root.querySelector('[data-retry]');
      this.reload = root.querySelector('[data-reload]');
      this.signIn = root.querySelector('[data-sign-in]');
      this.archive = root.querySelector('[data-archive]');
      this.pencil = root.querySelector('[data-pencil]');
      this.savedEntries = root.dataset.entries;
      this.savedNotes = root.dataset.notes;
      this.entries = [...this.savedEntries].map(Number);
      this.notes = decodeNotes(this.savedNotes);
      this.givens = [...root.dataset.givens].map(Number);
      this.revision = Number(root.dataset.revision);
      this.edits = 0;
      this.active = this.givens.indexOf(0);
      this.readonly = root.dataset.readonly === 'true';
      if (!this.readonly) {
        this.select(this.active);
        this.paint();
      }
      root.addEventListener('click', event => this.click(event));
      root.addEventListener('keydown', event => this.keydown(event));
      this.archive?.addEventListener('submit', event => this.submitArchive(event));
    }

    get changed() {
      return this.entries.join('') !== this.savedEntries || encodeNotes(this.notes) !== this.savedNotes;
    }

    get dirty() {
      return !this.readonly && (this.request || this.changed || this.uncertain);
    }

    select(index, focus = false) {
      const previous = this.cells[this.active];
      if (previous) {
        previous.tabIndex = -1;
        previous.removeAttribute('aria-current');
      }
      this.active = index;
      const cell = this.cells[index];
      cell.tabIndex = 0;
      cell.setAttribute('aria-current', 'true');
      if (focus) cell.focus({preventScroll: true});
    }

    click(event) {
      if (event.target.closest('[data-reload]')) {
        if (window.confirm('Discard unsaved moves and load the saved board?')) {
          this.dispose();
          editor = null;
          location.reload();
        }
        return;
      }
      if (event.target.closest('[data-retry]')) {
        this.failed = false;
        void this.flush();
        return;
      }
      if (this.readonly) return;
      const cell = event.target.closest('[data-cell]');
      const key = event.target.closest('[data-key]');
      if (cell) this.select(Number(cell.dataset.cell), true);
      else if (key) this.edit(Number(key.dataset.key));
      else if (event.target.closest('[data-clear]')) this.edit(0);
      else if (event.target.closest('[data-pencil]')) this.togglePencil();
    }

    keydown(event) {
      const cell = event.target.closest('[data-cell]');
      if (this.readonly || !cell || event.altKey || event.ctrlKey || event.metaKey) return;
      const index = Number(cell.dataset.cell);
      const row = Math.floor(index / 9);
      const column = index % 9;
      const offsets = {
        ArrowLeft: column > 0 ? -1 : 0,
        ArrowRight: column < 8 ? 1 : 0,
        ArrowUp: row > 0 ? -9 : 0,
        ArrowDown: row < 8 ? 9 : 0
      };
      if (Object.hasOwn(offsets, event.key)) this.select(index + offsets[event.key], true);
      else if (/^[1-9]$/.test(event.key)) this.edit(Number(event.key));
      else if (['Backspace', 'Delete', '0'].includes(event.key)) this.edit(0);
      else if (event.key.toLowerCase() === 'p') this.togglePencil();
      else return;
      event.preventDefault();
    }

    togglePencil() {
      this.pencil.setAttribute('aria-pressed', this.pencil.getAttribute('aria-pressed') === 'false' ? 'true' : 'false');
    }

    edit(value) {
      const index = this.active;
      if (this.givens[index] || this.readonly) return;
      const previousValue = this.entries[index];
      const previousNotes = this.notes[index];
      if (value && this.pencil.getAttribute('aria-pressed') === 'true') {
        if (this.entries[index]) return;
        this.notes[index] ^= 1 << (value - 1);
      } else {
        this.entries[index] = value;
        this.notes[index] = 0;
      }
      if (previousValue === this.entries[index] && previousNotes === this.notes[index]) return;
      this.edits++;
      this.paint();
      if (!this.failed) {
        this.status.textContent = this.dirty ? 'Saving…' : 'Saved';
        clearTimeout(this.timer);
        this.timer = setTimeout(() => void this.flush(), 200);
      }
    }

    paint() {
      for (const [i, cell] of this.cells.entries()) {
        const value = this.entries[i];
        const marks = cell.querySelector('[data-marks]');
        cell.querySelector('[data-digit]').textContent = digit(value);
        marks.hidden = Boolean(value);
        const notes = [];
        for (const [n, mark] of [...marks.children].entries()) {
          const present = this.notes[i] & (1 << n);
          mark.textContent = present ? n + 1 : '';
          if (present) notes.push(n + 1);
        }
        const row = Math.floor(i / 9);
        const column = i % 9;
        const conflicting = value && this.entries.some((other, j) => j !== i && other === value && (
          Math.floor(j / 9) === row || j % 9 === column ||
          Math.floor(j / 27) === Math.floor(row / 3) && Math.floor((j % 9) / 3) === Math.floor(column / 3)
        ));
        cell.classList.toggle('conflict', Boolean(conflicting && !this.givens[i]));
        const content = this.givens[i] ? 'clue ' + value : value || (notes.length ? 'pencil marks ' + notes.join(', ') : 'empty');
        cell.setAttribute('aria-label', 'Row ' + (row + 1) + ', column ' + (column + 1) + ', ' + content);
      }
    }

    fail(message, {conflict = false, auth = false} = {}) {
      this.failed = true;
      this.status.textContent = message;
      this.actions.hidden = false;
      this.retry.hidden = conflict;
      this.reload.hidden = !conflict;
      this.signIn.hidden = !auth;
    }

    async save() {
      const sent = {entries: this.entries.join(''), notes: encodeNotes(this.notes), revision: this.revision};
      const controller = new AbortController();
      this.controller = controller;
      const timeout = setTimeout(() => controller.abort(), 12000);
      this.status.textContent = 'Saving…';
      try {
        const response = await fetch(this.root.dataset.saveUrl, {
          method: 'POST', redirect: 'manual', signal: controller.signal,
          headers: {'Content-Type': 'application/json'}, body: JSON.stringify(sent)
        });
        if (this.disposed) return;
        if (response.status === 401 || response.type === 'opaqueredirect') {
          this.fail('Sign in again to save your moves.', {auth: true});
          return;
        }
        if (response.status === 409) {
          this.fail('The saved board changed. Your unsaved moves are still on this board.', {conflict: true});
          return;
        }
        if (!response.ok) {
          this.uncertain = response.status >= 500;
          this.fail('Could not save your moves. Try again.');
          return;
        }
        const {state} = await response.json();
        if (this.disposed) return;
        this.revision = state.revision;
        this.root.dataset.revision = String(state.revision);
        this.savedEntries = state.entries;
        this.savedNotes = state.notes;
        this.uncertain = false;
        this.actions.hidden = true;
        if (state.completed_at) this.complete(state);
        else this.status.textContent = this.changed ? 'Saving…' : 'Saved';
      } catch {
        if (!this.disposed) {
          this.uncertain = true;
          this.fail('Could not save your moves. Try again.');
        }
      } finally {
        clearTimeout(timeout);
        this.controller = null;
      }
    }

    async flush() {
      clearTimeout(this.timer);
      while (!this.failed && !this.disposed && this.dirty) {
        this.request ??= this.save().finally(() => { this.request = null; });
        await this.request;
      }
      if (!this.failed && !this.readonly && !this.disposed) {
        this.actions.hidden = true;
        this.status.textContent = 'Saved';
      }
      return !this.dirty;
    }

    complete(state) {
      this.readonly = true;
      this.root.dataset.completed = 'true';
      this.root.dataset.readonly = 'true';
      this.entries = [...state.entries].map(Number);
      this.notes = decodeNotes(state.notes);
      this.paint();
      this.controls.hidden = true;
      for (const cell of this.cells) {
        cell.disabled = true;
        cell.tabIndex = -1;
        cell.removeAttribute('aria-current');
      }
      if (this.archive) {
        this.archive.hidden = true;
        const back = this.root.querySelector('[data-back]');
        back.href = '/archive';
        back.textContent = '← Archive';
        window.htmx?.process(back);
        document.querySelector('.app-nav [aria-current="page"]')?.removeAttribute('aria-current');
        document.querySelector('.app-nav a[href="/archive"]')?.setAttribute('aria-current', 'page');
      }
      const next = this.root.querySelector('[data-next]');
      if (next) next.hidden = false;
      this.status.textContent = 'Solved';
      fireworks.play();
    }

    async submitArchive(event) {
      if (this.submitting || this.readonly) return;
      event.preventDefault();
      const button = this.archive.querySelector('button');
      button.disabled = true;
      const saved = await this.flush();
      button.disabled = false;
      if (!saved || this.disposed || this.readonly) return;
      this.archive.elements.revision.value = this.revision;
      this.submitting = true;
      this.archive.requestSubmit();
    }

    dispose() {
      fireworks.stop();
      this.disposed = true;
      clearTimeout(this.timer);
      this.controller?.abort();
    }
  }

  function paintPreview(board, values) {
    if (!board.children.length) {
      for (let i = 0; i < 81; i++) {
        const cell = document.createElement('span');
        cell.setAttribute('aria-hidden', 'true');
        board.append(cell);
      }
    }
    for (const [index, cell] of [...board.children].entries()) cell.textContent = digit(Number(values[index]));
  }

  let editor, previewed, longPress, heldTile;
  const tile = target => target.closest?.('[data-preview]');
  const tooltip = () => document.querySelector('#puzzle-tooltip');

  function positionPreview() {
    const tip = tooltip();
    if (!previewed || !tip || tip.hidden) return;
    const anchor = previewed.getBoundingClientRect();
    if (anchor.bottom < 0 || anchor.top > innerHeight) {
      hidePreview();
      return;
    }
    const {width, height} = tip.getBoundingClientRect();
    const left = Math.min(innerWidth - width - 8, Math.max(8, anchor.left + (anchor.width - width) / 2));
    const above = anchor.top - height - 7;
    const top = Math.min(innerHeight - height - 8, Math.max(8, above > 8 ? above : anchor.bottom + 7));
    tip.style.left = left + 'px';
    tip.style.top = top + 'px';
  }

  function showPreview(link) {
    const tip = tooltip();
    if (!tip || previewed === link) return;
    hidePreview();
    previewed = link;
    paintPreview(tip.firstElementChild, link.dataset.preview);
    link.setAttribute('aria-describedby', tip.id);
    tip.hidden = false;
    positionPreview();
  }

  function hidePreview() {
    previewed?.removeAttribute('aria-describedby');
    const tip = tooltip();
    if (tip) tip.hidden = true;
    previewed = null;
  }

  document.addEventListener('pointerover', event => {
    const link = tile(event.target);
    if (link && event.pointerType !== 'touch') showPreview(link);
  });
  document.addEventListener('pointerout', event => {
    if (event.pointerType !== 'touch' && previewed === tile(event.target) && !previewed?.contains(event.relatedTarget) && previewed !== document.activeElement) hidePreview();
  });
  document.addEventListener('focusin', event => {
    const link = tile(event.target);
    if (link) showPreview(link);
  });
  document.addEventListener('focusout', event => {
    if (previewed && previewed === tile(event.target)) hidePreview();
  });
  document.addEventListener('keydown', event => {
    if (event.key === 'Escape') hidePreview();
  });
  document.addEventListener('pointerdown', event => {
    const link = tile(event.target);
    heldTile = null;
    if (!link) hidePreview();
    if (link && event.pointerType === 'touch') {
      clearTimeout(longPress?.timer);
      const timer = setTimeout(() => {
        heldTile = link;
        showPreview(link);
      }, 450);
      longPress = {timer, pointer: event.pointerId, x: event.clientX, y: event.clientY};
    }
  });
  function cancelLongPress(event) {
    if (longPress?.pointer !== event.pointerId) return;
    clearTimeout(longPress.timer);
    longPress = null;
  }
  document.addEventListener('pointerup', cancelLongPress);
  document.addEventListener('pointercancel', cancelLongPress);
  document.addEventListener('pointermove', event => {
    if (longPress && Math.hypot(event.clientX - longPress.x, event.clientY - longPress.y) > 10) cancelLongPress(event);
  });
  document.addEventListener('click', event => {
    if (heldTile && heldTile === tile(event.target)) {
      event.preventDefault();
      event.stopImmediatePropagation();
      heldTile = null;
    }
  }, true);
  document.addEventListener('contextmenu', event => {
    if (heldTile === tile(event.target) && heldTile) event.preventDefault();
  });
  addEventListener('resize', positionPreview);
  addEventListener('scroll', positionPreview, {passive: true});

  function mount() {
    const root = document.querySelector('[data-play]');
    if (root && editor?.root !== root) editor = new Editor(root);
    const difficulty = document.querySelector('#difficulty');
    const example = document.querySelector('[data-example]');
    if (difficulty && !difficulty.dataset.ready) {
      difficulty.dataset.ready = 'true';
      difficulty.addEventListener('change', () => {
        const option = difficulty.selectedOptions[0];
        paintPreview(example, option.dataset.example);
        example.setAttribute('aria-label', option.textContent + ' example board');
      });
    }
  }

  // HTMX discards history snapshots. Keep entry indices so cancelling a
  // same-document Back or Forward restores its URL without losing the draft.
  function remember(index) {
    const state = {...history.state, sudokuIndex: index};
    history.replaceState(state, '');
    return {index, state, url: location.href};
  }

  let entry = remember(history.state?.sudokuIndex ?? 0);
  let restoring = false;
  document.addEventListener('htmx:beforeHistoryUpdate', () => { entry = remember(entry.index); });
  document.addEventListener('htmx:pushedIntoHistory', () => { entry = remember(entry.index + 1); });
  window.addEventListener('popstate', event => {
    if (restoring) {
      restoring = false;
      event.stopImmediatePropagation();
      return;
    }
    if (editor?.dirty) {
      if (!window.confirm(leaveMessage)) {
        event.stopImmediatePropagation();
        const index = event.state?.sudokuIndex;
        if (Number.isInteger(index) && index !== entry.index) {
          restoring = true;
          history.go(entry.index - index);
        } else {
          history.pushState(entry.state, '', entry.url);
        }
        return;
      }
      editor.dispose();
      editor = null;
    }
    entry = remember(event.state?.sudokuIndex ?? 0);
  }, true);

  window.addEventListener('beforeunload', event => {
    if (editor?.dirty) {
      event.preventDefault();
      event.returnValue = '';
    }
  });

  document.addEventListener('DOMContentLoaded', mount);
  const approved = new WeakMap();
  document.addEventListener('htmx:beforeRequest', event => {
    if (event.detail.target?.id !== 'workspace' || !editor?.dirty) return;
    if (window.confirm(leaveMessage)) approved.set(event.detail.xhr, {editor, edits: editor.edits});
    else event.preventDefault();
  });
  document.addEventListener('htmx:beforeSwap', event => {
    if (event.detail.target?.id !== 'workspace' || !event.detail.shouldSwap || !editor?.dirty) return;
    const permission = approved.get(event.detail.xhr);
    if (permission?.editor === editor && permission.edits === editor.edits) return;
    if (!window.confirm(leaveMessage)) event.preventDefault();
  });
  document.addEventListener('htmx:beforeCleanupElement', event => {
    if (editor?.root === event.detail.elt) {
      editor.dispose();
      editor = null;
    }
    if (event.detail.elt?.id === 'workspace') hidePreview();
  });
  document.addEventListener('htmx:afterSettle', event => {
    if (event.detail.target?.id === 'workspace') {
      mount();
      document.querySelector('#main')?.focus({preventScroll: true});
    }
  });

  function navigationFailed(event) {
    const destination = event.detail.elt?.closest('a[href]');
    const main = document.querySelector('#main');
    if (event.detail.target?.id !== 'workspace' || !destination || !main) return;
    let notice = main.querySelector('#navigation-error');
    if (!notice) {
      notice = document.createElement('p');
      notice.id = 'navigation-error';
      notice.className = 'error';
      notice.setAttribute('role', 'status');
      main.prepend(notice);
    }
    const retry = document.createElement('a');
    retry.href = destination.href;
    retry.setAttribute('hx-boost', 'false');
    retry.textContent = 'Open the page again';
    notice.replaceChildren('Unable to open this page. ', retry);
  }
  document.addEventListener('htmx:sendError', navigationFailed);
  document.addEventListener('htmx:responseError', navigationFailed);
  document.addEventListener('htmx:timeout', navigationFailed);
})();
