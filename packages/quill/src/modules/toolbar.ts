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

const groups = new WeakMap<HTMLElement, ToolbarGroup>();

// Every toolbar module initialized with the same container joins one group.
// The group owns the DOM listeners of the controls, so each control is bound
// once and its events are dispatched to the active toolbar only.
class ToolbarGroup {
  static find(container: HTMLElement) {
    let group = groups.get(container);
    if (group == null) {
      group = new ToolbarGroup(container);
      groups.set(container, group);
    }
    return group;
  }

  container: HTMLElement;
  controls: [string, HTMLElement][] = [];
  toolbars: Toolbar[] = [];
  active: Toolbar | null = null;
  shared = false;
  protected bindings = new Map<HTMLElement, () => void>();
  protected ignored = new WeakSet<HTMLElement>();
  protected disabledControls = new Set<HTMLElement>();
  protected observers: MutationObserver[] = [];

  constructor(container: HTMLElement) {
    this.container = container;
  }

  register(toolbar: Toolbar) {
    if (this.toolbars.includes(toolbar)) return;
    this.toolbars.push(toolbar);
    if (!this.shared && this.toolbars.length === 1) {
      this.active = toolbar;
      return;
    }
    this.shared = true;
    this.observe();
    this.syncEnabled();
  }

  activate(toolbar: Toolbar) {
    if (!this.shared || this.active === toolbar) return;
    if (!toolbar.quill.container.isConnected) return;
    this.register(toolbar);
    const previous = this.active;
    this.active = toolbar;
    toolbar.update(toolbar.quill.selection.getRange()[0]);
    this.syncEnabled();
    toolbar.quill.emitter.emit(Quill.events.TOOLBAR_ACTIVE_CHANGE, true);
    previous?.quill.emitter.emit(Quill.events.TOOLBAR_ACTIVE_CHANGE, false);
  }

  resolveActive() {
    if (this.shared) this.prune();
    return this.active;
  }

  prune() {
    const removed = this.toolbars.filter(
      (toolbar) => !toolbar.quill.container.isConnected,
    );
    if (removed.length === 0) return;
    this.toolbars = this.toolbars.filter(
      (toolbar) => !removed.includes(toolbar),
    );
    const previous = this.active;
    if (previous != null && removed.includes(previous)) {
      this.active = null;
      previous.update(null);
      this.syncEnabled();
      previous.quill.emitter.emit(Quill.events.TOOLBAR_ACTIVE_CHANGE, false);
    }
    if (this.toolbars.length === 0) {
      this.disconnect();
    }
  }

  bind(input: HTMLElement, format: string) {
    if (this.bindings.has(input)) return;
    const eventName = input.tagName === 'SELECT' ? 'change' : 'click';
    const listener = (e: Event) => {
      this.resolveActive()?.handleControl(input, format, e);
    };
    input.addEventListener(eventName, listener);
    this.bindings.set(input, () => {
      input.removeEventListener(eventName, listener);
    });
    this.controls.push([format, input]);
    this.syncEnabled();
  }

  unbind(input: HTMLElement) {
    const unbind = this.bindings.get(input);
    if (unbind == null) return;
    unbind();
    this.bindings.delete(input);
    const index = this.controls.findIndex(([, control]) => control === input);
    if (index >= 0) {
      this.controls.splice(index, 1);
    }
    if (this.disabledControls.delete(input)) {
      input.removeAttribute('disabled');
    }
  }

  reconcileControls() {
    Array.from(this.bindings.keys()).forEach((input) => {
      if (!this.container.contains(input)) {
        this.unbind(input);
      }
    });
    Array.from(
      this.container.querySelectorAll<HTMLElement>('button, select'),
    ).forEach((input) => {
      if (this.bindings.has(input) || this.ignored.has(input)) return;
      const bound = this.toolbars.some((toolbar) => {
        toolbar.attach(input);
        return this.bindings.has(input);
      });
      if (!bound) {
        this.ignored.add(input);
      }
    });
  }

  syncEnabled() {
    if (!this.shared) return;
    const disabled = this.active != null && !this.active.quill.isEnabled();
    this.controls.forEach(([, input]) => {
      if (disabled) {
        if (!input.hasAttribute('disabled')) {
          input.setAttribute('disabled', '');
          this.disabledControls.add(input);
        }
      } else if (this.disabledControls.delete(input)) {
        input.removeAttribute('disabled');
      }
    });
  }

  observe() {
    if (this.observers.length > 0) return;
    const editors = new MutationObserver(() => this.prune());
    editors.observe(document, { childList: true, subtree: true });
    const controls = new MutationObserver(() => this.reconcileControls());
    controls.observe(this.container, { childList: true, subtree: true });
    this.observers = [editors, controls];
  }

  disconnect() {
    this.observers.forEach((observer) => observer.disconnect());
    this.observers = [];
  }
}

class Toolbar extends Module<ToolbarProps> {
  static DEFAULTS: ToolbarProps;

  container?: HTMLElement | null;
  controls: [string, HTMLElement][];
  handlers: Record<string, Handler>;
  group: ToolbarGroup;

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
    this.group = ToolbarGroup.find(this.container);
    this.controls = this.group.controls;
    this.handlers = {};
    if (this.options.handlers) {
      Object.keys(this.options.handlers).forEach((format) => {
        const handler = this.options.handlers?.[format];
        if (handler) {
          this.addHandler(format, handler);
        }
      });
    }
    this.group.register(this);
    Array.from(this.container.querySelectorAll('button, select')).forEach(
      (input) => {
        // @ts-expect-error
        this.attach(input);
      },
    );
    this.quill.on(Quill.events.EDITOR_CHANGE, (type, newRange) => {
      if (this.group.shared) {
        if (type === Quill.events.SELECTION_CHANGE && newRange != null) {
          this.group.activate(this);
        }
        if (this.group.active !== this) return;
      }
      const [range] = this.quill.selection.getRange(); // quill.getSelection triggers update
      this.update(range);
    });
    this.quill.on(Quill.events.ENABLE_CHANGE, () => {
      if (this.group.active === this) {
        this.group.syncEnabled();
      }
    });
    this.quill.root.addEventListener('focus', () => {
      this.group.activate(this);
    });
  }

  addHandler(format: string, handler: Handler) {
    this.handlers[format] = handler;
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
    this.group.bind(input, format);
  }

  handleControl(input: HTMLElement, format: string, e: Event) {
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
    if (
      (this.group.shared && !this.quill.isEnabled()) ||
      (this.handlers[format] == null && this.quill.scroll.query(format) == null)
    ) {
      this.update(this.quill.selection.getRange()[0]);
      return;
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
