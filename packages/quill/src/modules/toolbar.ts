import Delta from 'quill-delta';
import { EmbedBlot, Scope } from 'parchment';
import Quill from '../core/quill.js';
import logger from '../core/logger.js';
import Module from '../core/module.js';
import type { Range } from '../core/selection.js';
import { syncPicker } from '../ui/picker.js';

const debug = logger('quill:toolbar');

type Handler = (this: Toolbar, value: any) => void;

export type ToolbarConfig = Array<
  string[] | Array<string | Record<string, unknown>>
>;
export interface ToolbarProps {
  container?: HTMLElement | ToolbarConfig | null;
  handlers?: Record<string, Handler>;
  option?: number;
  module?: boolean;
  theme?: boolean;
}

type ControlListener = {
  eventName: string;
  listener: EventListener;
  format: string;
};

class ToolbarSession {
  static sessions = new Set<ToolbarSession>();

  static pruning = false;

  controls: [string, HTMLElement][] = [];

  members = new Set<Toolbar>();

  active: Toolbar | null = null;

  shared = false;

  private listeners = new Map<HTMLElement, ControlListener>();

  private warned = new WeakSet<HTMLElement>();

  private connectedMembers = new WeakSet<Toolbar>();

  private observer: MutationObserver;

  private cleanups = new Map<Toolbar, () => void>();

  constructor(public container: HTMLElement) {
    ToolbarSession.sessions.add(this);
    installRemovalHook();
    this.observer = new MutationObserver(() => {
      this.syncControls();
    });
    this.observer.observe(this.container, {
      childList: true,
      subtree: true,
    });
    this.container.addEventListener('mousedown', this.onMouseDown);
  }

  add(toolbar: Toolbar) {
    this.members.add(toolbar);
    if (this.members.size > 1) {
      this.shared = true;
    }
    this.watchEditor(toolbar);
    this.syncControls();
    if (this.target() === toolbar) {
      const [range] = toolbar.quill.selection.getRange();
      toolbar.update(range);
    } else {
      this.syncChrome();
    }
  }

  activate(toolbar: Toolbar) {
    if (!this.members.has(toolbar) || !toolbar.isConnected()) return;
    this.active = toolbar;
    const [range] = toolbar.quill.selection.getRange();
    toolbar.update(range);
    this.syncChrome();
  }

  target(): Toolbar | null {
    this.pruneDisconnected();
    if (
      this.active &&
      this.members.has(this.active) &&
      this.active.isConnected()
    ) {
      return this.active;
    }
    if (!this.shared) {
      const [only] = this.members;
      if (only?.isConnected()) return only;
    }
    return null;
  }

  supports(format: string) {
    return Array.from(this.members).some((toolbar) => {
      return (
        toolbar.handlers[format] != null ||
        toolbar.quill.scroll.query(format) != null
      );
    });
  }

  addCleanup(toolbar: Toolbar, cleanup: () => void) {
    const previous = this.cleanups.get(toolbar);
    this.cleanups.set(toolbar, () => {
      previous?.();
      cleanup();
    });
  }

  unregister(toolbar: Toolbar) {
    if (!this.members.has(toolbar)) return;
    const wasActive = this.active === toolbar;
    this.members.delete(toolbar);
    if (wasActive) this.active = null;
    this.cleanups.get(toolbar)?.();
    this.cleanups.delete(toolbar);
    if (wasActive) {
      this.clearActiveState();
      this.removeManagedFileInputs();
    }
    if (this.members.size === 0) {
      this.destroy();
    }
  }

  pruneDisconnected() {
    if (ToolbarSession.pruning) return;
    ToolbarSession.pruning = true;
    try {
      Array.from(this.members).forEach((toolbar) => {
        if (toolbar.isConnected()) {
          this.connectedMembers.add(toolbar);
          return;
        }
        if (this.connectedMembers.has(toolbar)) {
          this.unregister(toolbar);
        }
      });
    } finally {
      ToolbarSession.pruning = false;
    }
  }

  static pruneAll() {
    if (ToolbarSession.pruning) return;
    Array.from(ToolbarSession.sessions).forEach((session) => {
      session.pruneDisconnected();
    });
  }

  syncChrome() {
    const current = this.target();
    const disabled = current ? !current.quill.isEnabled() : false;
    this.controls.forEach(([, input]) => {
      if (
        input instanceof HTMLButtonElement ||
        input instanceof HTMLSelectElement
      ) {
        input.disabled = disabled;
      }
      if (input instanceof HTMLSelectElement) {
        syncPicker(input);
      }
    });
    this.syncManagedFileInput();
  }

  syncManagedFileInput() {
    const inputs = Array.from(
      this.container.querySelectorAll<HTMLInputElement>(
        'input.ql-image[type=file]',
      ),
    );
    const quill = this.target()?.quill;
    if (quill == null) {
      if (this.shared) {
        inputs.forEach((input) => input.remove());
      }
      return;
    }
    const types = quill.uploader?.options?.mimetypes;
    inputs.forEach((input) => {
      if (Array.isArray(types)) {
        input.setAttribute('accept', types.join(', '));
      }
      input.disabled = !quill.isEnabled();
    });
  }

  private clearActiveState() {
    this.controls.forEach(([, input]) => {
      if (input instanceof HTMLSelectElement) {
        input.disabled = false;
        input.value = '';
        input.selectedIndex = -1;
        syncPicker(input);
      } else if (input instanceof HTMLButtonElement) {
        input.disabled = false;
        input.classList.remove('ql-active');
        input.setAttribute('aria-pressed', 'false');
      }
    });
  }

  private removeManagedFileInputs() {
    this.container
      .querySelectorAll('input.ql-image[type=file]')
      .forEach((input) => input.remove());
  }

  private destroy() {
    this.observer.disconnect();
    this.container.removeEventListener('mousedown', this.onMouseDown);
    this.listeners.forEach(({ eventName, listener }, input) => {
      input.removeEventListener(eventName, listener);
    });
    this.listeners.clear();
    this.controls.splice(0, this.controls.length);
    ToolbarSession.sessions.delete(this);
    sessionByContainer.delete(this.container);
  }

  private watchEditor(toolbar: Toolbar) {
    const { quill } = toolbar;
    const onEditorChange = (
      type: string,
      range: Range | null,
      _oldRange: Range | null,
      source: string,
    ) => {
      if (!toolbar.isConnected()) {
        this.unregister(toolbar);
        return;
      }
      if (
        type === Quill.events.SELECTION_CHANGE &&
        source === Quill.sources.USER &&
        range != null
      ) {
        this.activate(toolbar);
        return;
      }
      if (this.target() === toolbar) {
        const [current] = quill.selection.getRange();
        toolbar.update(current);
      }
    };
    const onFocus = () => {
      if (!toolbar.isConnected()) return;
      this.activate(toolbar);
    };
    quill.on(Quill.events.EDITOR_CHANGE, onEditorChange);
    quill.root.addEventListener('focusin', onFocus);
    const originalEnable = quill.enable.bind(quill);
    quill.enable = (enabled = true) => {
      originalEnable(enabled);
      if (!this.members.has(toolbar)) return;
      if (this.target() === toolbar) {
        const [range] = quill.selection.getRange();
        toolbar.update(range);
        this.syncChrome();
      }
    };
    this.addCleanup(toolbar, () => {
      quill.off(Quill.events.EDITOR_CHANGE, onEditorChange);
      quill.root.removeEventListener('focusin', onFocus);
      quill.enable = originalEnable;
    });
  }

  private onMouseDown = (event: MouseEvent) => {
    if (!this.shared) return;
    const target = event.target;
    if (!(target instanceof Element)) return;
    const control = target.closest('button, select, .ql-picker');
    if (control && this.container.contains(control)) {
      event.preventDefault();
    }
  };

  rescan(input?: HTMLElement) {
    if (input) this.bindControl(input);
    else this.syncControls();
  }

  private syncControls() {
    for (let i = this.controls.length - 1; i >= 0; i -= 1) {
      if (!this.container.contains(this.controls[i][1])) {
        this.controls.splice(i, 1);
      }
    }
    Array.from(this.container.querySelectorAll('button, select')).forEach(
      (input) => {
        this.bindControl(input as HTMLElement);
      },
    );
    if (this.target()) this.syncChrome();
  }

  private bindControl(input: HTMLElement) {
    const existing = this.listeners.get(input);
    const format = existing?.format ?? formatName(input);
    if (!format) return;
    if (input.tagName === 'BUTTON') {
      input.setAttribute('type', 'button');
    }
    if (!existing) {
      if (!this.supports(format)) {
        if (!this.warned.has(input)) {
          debug.warn('ignoring attaching to nonexistent format', format, input);
          this.warned.add(input);
        }
        return;
      }
      const eventName = input.tagName === 'SELECT' ? 'change' : 'click';
      const listener: EventListener = (event) => {
        this.handleControl(input, format, event);
      };
      input.addEventListener(eventName, listener);
      this.listeners.set(input, { eventName, listener, format });
    }
    if (!this.controls.some(([, control]) => control === input)) {
      this.controls.push([format, input]);
    }
  }

  private handleControl(input: HTMLElement, format: string, event: Event) {
    const focused = Array.from(this.members).find((toolbar) => {
      return toolbar.isConnected() && toolbar.quill.hasFocus();
    });
    if (focused) this.activate(focused);
    const toolbar = this.target();
    if (input.tagName !== 'SELECT') {
      event.preventDefault();
    }
    if (toolbar == null || !toolbar.quill.isEnabled()) {
      if (toolbar) {
        const [range] = toolbar.quill.selection.getRange();
        toolbar.update(range);
      }
      return;
    }
    let value: unknown;
    if (input.tagName === 'SELECT') {
      const select = input as HTMLSelectElement;
      if (select.selectedIndex < 0) return;
      const selected = select.options[select.selectedIndex];
      if (selected.hasAttribute('selected')) {
        value = false;
      } else {
        value = selected.value || false;
      }
    } else if (input.classList.contains('ql-active')) {
      value = false;
    } else {
      value =
        (input as HTMLButtonElement).value || !input.hasAttribute('value');
    }
    const { quill } = toolbar;
    quill.focus();
    const [range] = quill.selection.getRange();
    const blot = quill.scroll.query(format);
    if (toolbar.handlers[format] != null) {
      toolbar.handlers[format].call(toolbar, value);
    } else if (
      blot != null &&
      // @ts-expect-error
      blot.prototype instanceof EmbedBlot
    ) {
      value = prompt(`Enter ${format}`); // eslint-disable-line no-alert
      if (!value) return;
      quill.updateContents(
        new Delta()
          // @ts-expect-error Fix me later
          .retain(range.index)
          // @ts-expect-error Fix me later
          .delete(range.length)
          .insert({ [format]: value }),
        Quill.sources.USER,
      );
    } else {
      quill.format(format, value, Quill.sources.USER);
    }
    toolbar.update(range);
  }
}

const sessionByContainer = new WeakMap<HTMLElement, ToolbarSession>();

function sessionFor(container: HTMLElement) {
  let session = sessionByContainer.get(container);
  if (session == null || !ToolbarSession.sessions.has(session)) {
    session = new ToolbarSession(container);
    sessionByContainer.set(container, session);
  }
  return session;
}

function formatName(input: HTMLElement) {
  const className = Array.from(input.classList).find((name) => {
    return name.indexOf('ql-') === 0 && name !== 'ql-active';
  });
  return className ? className.slice('ql-'.length) : null;
}

let removalHookInstalled = false;
let refreshingToolbars = false;

function refreshToolbarsContaining(node: Node | null) {
  if (node == null || refreshingToolbars || ToolbarSession.pruning) {
    return;
  }
  refreshingToolbars = true;
  try {
    ToolbarSession.sessions.forEach((session) => {
      if (session.container === node || session.container.contains(node)) {
        session.rescan();
      }
    });
  } finally {
    refreshingToolbars = false;
  }
}

function installRemovalHook() {
  if (removalHookInstalled) return;
  removalHookInstalled = true;
  const nativeRemoveChild = Node.prototype.removeChild;
  const nativeAppendChild = Node.prototype.appendChild;
  const nativeInsertBefore = Node.prototype.insertBefore;
  const nativeRemove = Element.prototype.remove;
  Node.prototype.removeChild = function removeChild<T extends Node>(
    this: Node,
    child: T,
  ): T {
    const removed = nativeRemoveChild.call(this, child) as T;
    ToolbarSession.pruneAll();
    refreshToolbarsContaining(this);
    return removed;
  };
  // element.remove() detaches in the DOM engine without calling the JS
  // removeChild override, so editor removal has to be observed here too.
  Element.prototype.remove = function remove(this: Element) {
    nativeRemove.call(this);
    ToolbarSession.pruneAll();
  };
  Node.prototype.appendChild = function appendChild<T extends Node>(
    this: Node,
    child: T,
  ): T {
    const appended = nativeAppendChild.call(this, child) as T;
    refreshToolbarsContaining(this);
    return appended;
  };
  Node.prototype.insertBefore = function insertBefore<T extends Node>(
    this: Node,
    child: T,
    ref: Node | null,
  ): T {
    const inserted = nativeInsertBefore.call(this, child, ref) as T;
    refreshToolbarsContaining(this);
    return inserted;
  };
  const removalObserver = new MutationObserver(() => {
    ToolbarSession.pruneAll();
  });
  removalObserver.observe(document.documentElement, {
    childList: true,
    subtree: true,
  });
}

export function ensureImageFileInput(
  container: HTMLElement,
  fallback: Quill,
): HTMLInputElement | null {
  const session = sessionByContainer.get(container);
  const quill = session ? session.target()?.quill : fallback;
  if (quill == null || !quill.isEnabled()) return null;
  let fileInput = container.querySelector<HTMLInputElement>(
    'input.ql-image[type=file]',
  );
  if (fileInput == null) {
    fileInput = document.createElement('input');
    fileInput.setAttribute('type', 'file');
    fileInput.classList.add('ql-image');
    fileInput.addEventListener('change', () => {
      const active = sessionByContainer.get(container)?.target()?.quill ?? null;
      if (active == null || !active.isEnabled()) {
        if (fileInput) fileInput.value = '';
        return;
      }
      const range = active.getSelection(true);
      active.uploader.upload(range, fileInput?.files);
      if (fileInput) fileInput.value = '';
    });
    container.appendChild(fileInput);
  }
  const types = quill.uploader?.options?.mimetypes;
  if (Array.isArray(types)) {
    fileInput.setAttribute('accept', types.join(', '));
  }
  fileInput.disabled = false;
  return fileInput;
}

class Toolbar extends Module<ToolbarProps> {
  static DEFAULTS: ToolbarProps;

  container?: HTMLElement | null;
  controls: [string, HTMLElement][];
  handlers: Record<string, Handler>;

  private session?: ToolbarSession;

  constructor(quill: Quill, options: Partial<ToolbarProps>) {
    super(quill, options);
    if (Array.isArray(this.options.container)) {
      const container = document.createElement('div');
      container.setAttribute('role', 'toolbar');
      addControls(container, this.options.container);
      quill.container?.parentNode?.insertBefore(container, quill.container);
      this.container = container;
    } else if (typeof this.options.container === 'string') {
      this.container = document.querySelector(this.options.container);
    } else {
      this.container = this.options.container;
    }
    if (!(this.container instanceof HTMLElement)) {
      debug.error('Container required for toolbar', this.options);
      return;
    }
    this.container.classList.add('ql-toolbar');
    this.handlers = {};
    if (this.options.handlers) {
      Object.keys(this.options.handlers).forEach((format) => {
        const handler = this.options.handlers?.[format];
        if (handler) {
          this.addHandler(format, handler);
        }
      });
    }
    this.session = sessionFor(this.container);
    this.controls = this.session.controls;
    this.session.add(this);
  }

  addCleanup(cleanup: () => void) {
    this.session?.addCleanup(this, cleanup);
  }

  addHandler(format: string, handler: Handler) {
    this.handlers[format] = handler;
    this.session?.rescan();
  }

  attach(input: HTMLElement) {
    this.session?.rescan(input);
  }

  isConnected() {
    return (
      this.quill != null &&
      this.quill.container?.isConnected === true &&
      this.quill.root?.isConnected === true
    );
  }

  isToolbarActive() {
    if (this.session == null) return true;
    return this.session.target() === this;
  }

  update(range: Range | null) {
    const formats = range == null ? {} : this.quill.getFormat(range);
    this.controls.forEach((pair) => {
      const [format, input] = pair;
      if (input.tagName === 'SELECT') {
        let option: HTMLOptionElement | null = null;
        if (range == null) {
          option = null;
        } else if (formats[format] == null) {
          option = input.querySelector('option[selected]');
        } else if (!Array.isArray(formats[format])) {
          let value = formats[format];
          if (typeof value === 'string') {
            value = value.replace(/"/g, '\\"');
          }
          option = input.querySelector(`option[value="${value}"]`);
        }
        if (option == null) {
          // @ts-expect-error TODO fix me later
          input.value = ''; // TODO make configurable?
          // @ts-expect-error TODO fix me later
          input.selectedIndex = -1;
        } else {
          option.selected = true;
        }
      } else if (range == null) {
        input.classList.remove('ql-active');
        input.setAttribute('aria-pressed', 'false');
      } else if (input.hasAttribute('value')) {
        // both being null should match (default values)
        // '1' should match with 1 (headers)
        const value = formats[format] as boolean | number | string | object;
        const isActive =
          value === input.getAttribute('value') ||
          (value != null && value.toString() === input.getAttribute('value')) ||
          (value == null && !input.getAttribute('value'));
        input.classList.toggle('ql-active', isActive);
        input.setAttribute('aria-pressed', isActive.toString());
      } else {
        const isActive = formats[format] != null;
        input.classList.toggle('ql-active', isActive);
        input.setAttribute('aria-pressed', isActive.toString());
      }
      if (input instanceof HTMLSelectElement) {
        syncPicker(input);
      }
    });
    this.session?.syncChrome();
  }
}
Toolbar.DEFAULTS = {};

function addButton(container: HTMLElement, format: string, value?: string) {
  const input = document.createElement('button');
  input.setAttribute('type', 'button');
  input.classList.add(`ql-${format}`);
  input.setAttribute('aria-pressed', 'false');
  if (value != null) {
    input.value = value;
    input.setAttribute('aria-label', `${format}: ${value}`);
  } else {
    input.setAttribute('aria-label', format);
  }
  container.appendChild(input);
}

function addControls(
  container: HTMLElement,
  groups:
    | (string | Record<string, unknown>)[][]
    | (string | Record<string, unknown>)[],
) {
  if (!Array.isArray(groups[0])) {
    // @ts-expect-error
    groups = [groups];
  }
  groups.forEach((controls: any) => {
    const group = document.createElement('span');
    group.classList.add('ql-formats');
    controls.forEach((control: any) => {
      if (typeof control === 'string') {
        addButton(group, control);
      } else {
        const format = Object.keys(control)[0];
        const value = control[format];
        if (Array.isArray(value)) {
          addSelect(group, format, value);
        } else {
          addButton(group, format, value);
        }
      }
    });
    container.appendChild(group);
  });
}

function addSelect(
  container: HTMLElement,
  format: string,
  values: Array<string | boolean>,
) {
  const input = document.createElement('select');
  input.classList.add(`ql-${format}`);
  values.forEach((value) => {
    const option = document.createElement('option');
    if (value !== false) {
      option.setAttribute('value', String(value));
    } else {
      option.setAttribute('selected', 'selected');
    }
    input.appendChild(option);
  });
  container.appendChild(input);
}

Toolbar.DEFAULTS = {
  container: null,
  handlers: {
    clean() {
      const range = this.quill.getSelection();
      if (range == null) return;
      if (range.length === 0) {
        const formats = this.quill.getFormat();
        Object.keys(formats).forEach((name) => {
          // Clean functionality in existing apps only clean inline formats
          if (this.quill.scroll.query(name, Scope.INLINE) != null) {
            this.quill.format(name, false, Quill.sources.USER);
          }
        });
      } else {
        this.quill.removeFormat(range.index, range.length, Quill.sources.USER);
      }
    },
    direction(value) {
      const { align } = this.quill.getFormat();
      if (value === 'rtl' && align == null) {
        this.quill.format('align', 'right', Quill.sources.USER);
      } else if (!value && align === 'right') {
        this.quill.format('align', false, Quill.sources.USER);
      }
      this.quill.format('direction', value, Quill.sources.USER);
    },
    indent(value) {
      const range = this.quill.getSelection();
      // @ts-expect-error
      const formats = this.quill.getFormat(range);
      // @ts-expect-error
      const indent = parseInt(formats.indent || 0, 10);
      if (value === '+1' || value === '-1') {
        let modifier = value === '+1' ? 1 : -1;
        if (formats.direction === 'rtl') modifier *= -1;
        this.quill.format('indent', indent + modifier, Quill.sources.USER);
      }
    },
    link(value) {
      if (value === true) {
        value = prompt('Enter link URL:'); // eslint-disable-line no-alert
      }
      this.quill.format('link', value, Quill.sources.USER);
    },
    list(value) {
      const range = this.quill.getSelection();
      // @ts-expect-error
      const formats = this.quill.getFormat(range);
      if (value === 'check') {
        if (formats.list === 'checked' || formats.list === 'unchecked') {
          this.quill.format('list', false, Quill.sources.USER);
        } else {
          this.quill.format('list', 'unchecked', Quill.sources.USER);
        }
      } else {
        this.quill.format('list', value, Quill.sources.USER);
      }
    },
  },
};

export { Toolbar as default, addControls };
