import Delta from 'quill-delta';
import { EmbedBlot, Scope } from 'parchment';
import Quill from '../core/quill.js';
import logger from '../core/logger.js';
import Module from '../core/module.js';
import type { Range } from '../core/selection.js';

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

const isControl = (node: Node): node is HTMLElement =>
  node instanceof HTMLElement &&
  (node.tagName === 'BUTTON' || node.tagName === 'SELECT');

const findControls = (node: Node): HTMLElement[] => {
  if (!(node instanceof HTMLElement)) return [];
  const controls = Array.from(
    node.querySelectorAll<HTMLElement>('button, select'),
  );
  return isControl(node) ? [node, ...controls] : controls;
};

// Coordinates every Toolbar module attached to the same container element so
// that each control is wired once and only the active editor receives actions.
class SharedToolbar {
  static instances = new WeakMap<HTMLElement, SharedToolbar>();

  static for(container: HTMLElement) {
    let shared = SharedToolbar.instances.get(container);
    if (shared == null) {
      shared = new SharedToolbar(container);
      SharedToolbar.instances.set(container, shared);
    }
    return shared;
  }

  container: HTMLElement;
  active: Toolbar | null = null;
  toolbars = new Set<Toolbar>();
  private listeners = new Map<HTMLElement, EventListener>();
  private disabledControls = new Set<HTMLElement>();
  private connected = new WeakSet<Toolbar>();
  private observer: MutationObserver | null = null;

  constructor(container: HTMLElement) {
    this.container = container;
  }

  register(toolbar: Toolbar) {
    if (this.toolbars.has(toolbar)) return;
    this.toolbars.add(toolbar);
    toolbar.controls = toolbar.controls.filter(
      ([, input]) =>
        this.listeners.has(input) || this.container.contains(input),
    );
    findControls(this.container).forEach((input) => toolbar.attach(input));
    this.observe();
    this.prune();
    if (this.active == null) {
      this.active = toolbar;
    }
  }

  activate(toolbar: Toolbar) {
    if (!this.toolbars.has(toolbar)) this.register(toolbar);
    this.prune();
    if (this.active === toolbar) return;
    this.active = toolbar;
    this.refresh();
  }

  bind(input: HTMLElement) {
    if (this.listeners.has(input)) return;
    const listener = (event: Event) => this.dispatch(input, event);
    input.addEventListener(
      input.tagName === 'SELECT' ? 'change' : 'click',
      listener,
    );
    this.listeners.set(input, listener);
  }

  unbind(input: HTMLElement) {
    const listener = this.listeners.get(input);
    if (listener != null) {
      input.removeEventListener(
        input.tagName === 'SELECT' ? 'change' : 'click',
        listener,
      );
      this.listeners.delete(input);
    }
    if (this.disabledControls.delete(input)) {
      input.removeAttribute('disabled');
    }
    this.toolbars.forEach((toolbar) => toolbar.detach(input));
  }

  isDisabled() {
    return this.active != null && !this.active.quill.isEnabled();
  }

  syncEnabled() {
    const disabled = this.isDisabled();
    this.listeners.forEach((_listener, input) => {
      if (!isControl(input)) return;
      if (disabled) {
        if (!input.hasAttribute('disabled')) {
          input.setAttribute('disabled', '');
          this.disabledControls.add(input);
        }
      } else if (this.disabledControls.delete(input)) {
        input.removeAttribute('disabled');
      }
    });
    this.toolbars.forEach((toolbar) => toolbar.notify());
  }

  private refresh() {
    this.listeners.forEach((_listener, input) => resetControl(input));
    if (this.active != null) {
      const [range] = this.active.quill.selection.getRange();
      this.active.update(range);
    }
    this.syncEnabled();
  }

  private dispatch(input: HTMLElement, event: Event) {
    this.prune();
    const toolbar = this.active;
    if (toolbar == null || !toolbar.quill.isEnabled()) {
      if (input.tagName !== 'SELECT') event.preventDefault();
      return;
    }
    const control = toolbar.controls.find(([, other]) => other === input);
    if (control == null) return;
    toolbar.handleControl(control[0], input, event);
  }

  private observe() {
    if (this.observer != null || typeof MutationObserver === 'undefined') {
      return;
    }
    this.observer = new MutationObserver((records) => {
      this.prune();
      if (this.toolbars.size === 0) return;
      const candidates = new Set<HTMLElement>();
      records.forEach((record) => {
        if (!this.container.contains(record.target)) return;
        [
          ...Array.from(record.addedNodes),
          ...Array.from(record.removedNodes),
        ].forEach((node) => {
          findControls(node).forEach((input) => candidates.add(input));
        });
      });
      if (candidates.size === 0) return;
      candidates.forEach((input) => {
        if (this.container.contains(input)) {
          this.toolbars.forEach((toolbar) => toolbar.attach(input));
        } else {
          this.unbind(input);
        }
      });
      if (this.active != null) {
        const [range] = this.active.quill.selection.getRange();
        this.active.update(range);
      }
      this.syncEnabled();
    });
    this.observer.observe(document, { childList: true, subtree: true });
  }

  // Editors have no teardown API, so an editor that was once in the document
  // and has since been detached is treated as removed.
  private prune() {
    let activeRemoved = false;
    this.toolbars.forEach((toolbar) => {
      if (toolbar.quill.container.isConnected) {
        this.connected.add(toolbar);
      } else if (this.connected.has(toolbar)) {
        this.toolbars.delete(toolbar);
        if (this.active === toolbar) {
          this.active = null;
          activeRemoved = true;
        }
      }
    });
    if (activeRemoved) this.refresh();
    if (this.toolbars.size === 0) {
      Array.from(this.listeners.keys()).forEach((input) => this.unbind(input));
      this.observer?.disconnect();
      this.observer = null;
    }
  }
}

function resetControl(input: HTMLElement) {
  if (input.tagName === 'SELECT') {
    (input as HTMLSelectElement).value = '';
    (input as HTMLSelectElement).selectedIndex = -1;
  } else {
    input.classList.remove('ql-active');
    input.setAttribute('aria-pressed', 'false');
  }
}

class Toolbar extends Module<ToolbarProps> {
  static DEFAULTS: ToolbarProps;

  container?: HTMLElement | null;
  controls: [string, HTMLElement][];
  handlers: Record<string, Handler>;
  shared: SharedToolbar;
  private stateListeners: (() => void)[] = [];

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
    this.controls = [];
    this.handlers = {};
    if (this.options.handlers) {
      Object.keys(this.options.handlers).forEach((format) => {
        const handler = this.options.handlers?.[format];
        if (handler) {
          this.addHandler(format, handler);
        }
      });
    }
    this.shared = SharedToolbar.for(this.container);
    this.shared.register(this);
    this.quill.on(Quill.events.EDITOR_CHANGE, (type) => {
      const [range] = this.quill.selection.getRange(); // quill.getSelection triggers update
      if (type === Quill.events.SELECTION_CHANGE && range != null) {
        this.shared.activate(this);
      }
      if (this.shared.active === this) {
        this.update(range);
      }
    });
    this.quill.root.addEventListener('focus', () => {
      this.shared.activate(this);
    });
    this.quill.on(Quill.events.ENABLE_CHANGE, () => {
      if (this.shared.active === this) {
        this.shared.syncEnabled();
      }
    });
  }

  isActive() {
    return this.shared != null && this.shared.active === this;
  }

  onStateChange(listener: () => void) {
    this.stateListeners.push(listener);
  }

  notify() {
    this.stateListeners.forEach((listener) => listener());
  }

  addHandler(format: string, handler: Handler) {
    this.handlers[format] = handler;
  }

  detach(input: HTMLElement) {
    this.controls = this.controls.filter(([, other]) => other !== input);
  }

  attach(input: HTMLElement) {
    let format = Array.from(input.classList).find((className) => {
      return className.indexOf('ql-') === 0;
    });
    if (!format) return;
    format = format.slice('ql-'.length);
    if (input.tagName === 'BUTTON') {
      input.setAttribute('type', 'button');
    }
    if (
      this.handlers[format] == null &&
      this.quill.scroll.query(format) == null
    ) {
      debug.warn('ignoring attaching to nonexistent format', format, input);
      return;
    }
    if (!this.controls.some(([, other]) => other === input)) {
      this.controls.push([format, input]);
    }
    this.shared.bind(input);
  }

  handleControl(format: string, input: HTMLElement, e: Event) {
    let value;
    if (input.tagName === 'SELECT') {
      // @ts-expect-error
      if (input.selectedIndex < 0) return;
      // @ts-expect-error
      const selected = input.options[input.selectedIndex];
      if (selected.hasAttribute('selected')) {
        value = false;
      } else {
        value = selected.value || false;
      }
    } else {
      if (input.classList.contains('ql-active')) {
        value = false;
      } else {
        // @ts-expect-error
        value = input.value || !input.hasAttribute('value');
      }
      e.preventDefault();
    }
    this.quill.focus();
    const [range] = this.quill.selection.getRange();
    if (this.handlers[format] != null) {
      this.handlers[format].call(this, value);
    } else if (
      // @ts-expect-error
      this.quill.scroll.query(format).prototype instanceof EmbedBlot
    ) {
      value = prompt(`Enter ${format}`); // eslint-disable-line no-alert
      if (!value) return;
      this.quill.updateContents(
        new Delta()
          // @ts-expect-error Fix me later
          .retain(range.index)
          // @ts-expect-error Fix me later
          .delete(range.length)
          .insert({ [format]: value }),
        Quill.sources.USER,
      );
    } else {
      this.quill.format(format, value, Quill.sources.USER);
    }
    this.update(range);
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
    });
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
