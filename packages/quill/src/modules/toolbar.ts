import Delta from 'quill-delta';
import { EmbedBlot, Scope } from 'parchment';
import Quill from '../core/quill.js';
import logger from '../core/logger.js';
import Module from '../core/module.js';
import type { Range } from '../core/selection.js';
import { getPicker } from '../ui/picker.js';

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

class Toolbar extends Module<ToolbarProps> {
  static DEFAULTS: ToolbarProps;

  container?: HTMLElement | null;
  controls: [string, HTMLElement][];
  handlers: Record<string, Handler>;

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
    const group = getGroup(this.container);
    this.controls = group.controls;
    group.toolbars.add(this);
    if (group.active == null || !isLive(group.active)) {
      group.active = this;
    }
    Array.from(this.container.querySelectorAll('button, select')).forEach(
      (input) => {
        // @ts-expect-error
        this.attach(input);
      },
    );
    this.quill.on(Quill.events.EDITOR_CHANGE, (type, range) => {
      if (
        group.active !== this &&
        type === Quill.events.SELECTION_CHANGE &&
        range != null
      ) {
        activate(group, this);
        return;
      }
      if (group.active !== this) return;
      const [current] = this.quill.selection.getRange(); // quill.getSelection triggers update
      this.update(current);
    });
    this.quill.root.addEventListener('focus', () => {
      if (group.active !== this) activate(group, this);
    });
    if (group.active === this) syncDisabled(group);
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
    // @ts-expect-error
    const group = getGroup(this.container);
    if (group.listeners.has(input)) return;
    const eventName = input.tagName === 'SELECT' ? 'change' : 'click';
    const name = format;
    const listener = (e: Event) => {
      const toolbar = getActive(group);
      if (toolbar == null || !toolbar.quill.isEnabled()) {
        e.preventDefault();
        return;
      }
      toolbar.handle(name, input, e);
    };
    input.addEventListener(eventName, listener);
    group.listeners.set(input, { eventName, listener });
    this.controls.push([format, input]);
  }

  detach(input: HTMLElement) {
    // @ts-expect-error
    const group = getGroup(this.container);
    const entry = group.listeners.get(input);
    if (entry == null) return;
    input.removeEventListener(entry.eventName, entry.listener);
    group.listeners.delete(input);
    const index = group.controls.findIndex(([, control]) => control === input);
    if (index >= 0) group.controls.splice(index, 1);
  }

  handle(format: string, input: HTMLElement, e: Event) {
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
      const picker = input.tagName === 'SELECT' ? getPicker(input) : null;
      if (picker != null) picker.update();
    });
  }
}
Toolbar.DEFAULTS = {};

interface ToolbarGroup {
  container: HTMLElement;
  toolbars: Set<Toolbar>;
  active: Toolbar | null;
  controls: [string, HTMLElement][];
  listeners: Map<
    HTMLElement,
    { eventName: string; listener: (e: Event) => void }
  >;
  observer: MutationObserver | null;
}

const groups = new WeakMap<HTMLElement, ToolbarGroup>();

function isLive(toolbar: Toolbar) {
  return toolbar.quill.root.isConnected;
}

function getGroup(container: HTMLElement): ToolbarGroup {
  let group = groups.get(container);
  if (group == null) {
    group = {
      container,
      toolbars: new Set(),
      active: null,
      controls: [],
      listeners: new Map(),
      observer: null,
    };
    groups.set(container, group);
    observe(group);
  }
  return group;
}

function getActive(group: ToolbarGroup) {
  prune(group);
  return group.active;
}

function prune(group: ToolbarGroup) {
  group.toolbars.forEach((toolbar) => {
    if (!isLive(toolbar)) group.toolbars.delete(toolbar);
  });
  if (group.active != null && !isLive(group.active)) {
    group.active = null;
    const fileInput = group.container.querySelector(
      'input.ql-image[type=file]',
    );
    if (fileInput != null) fileInput.remove();
    group.controls.forEach(([, input]) => {
      if (input.tagName === 'SELECT') {
        const picker = getPicker(input);
        if (picker != null) picker.close();
      }
    });
    Toolbar.prototype.update.call({ controls: group.controls } as any, null);
    syncDisabled(group);
  }
  if (group.toolbars.size === 0 && group.observer != null) {
    group.observer.disconnect();
    group.observer = null;
    group.listeners.forEach(({ eventName, listener }, input) => {
      input.removeEventListener(eventName, listener);
    });
    group.listeners.clear();
    group.controls.splice(0, group.controls.length);
    groups.delete(group.container);
  }
}

function activate(group: ToolbarGroup, toolbar: Toolbar) {
  if (!isLive(toolbar)) return;
  group.active = toolbar;
  prune(group);
  const fileInput = group.container.querySelector(
    'input.ql-image[type=file]',
  );
  const uploader = (toolbar.quill as any).uploader;
  if (fileInput != null && uploader?.options?.mimetypes) {
    fileInput.setAttribute('accept', uploader.options.mimetypes.join(', '));
  }
  syncDisabled(group);
  const [range] = toolbar.quill.selection.getRange();
  toolbar.update(range);
}

function syncDisabled(group: ToolbarGroup) {
  const active = group.active;
  const disabled = active == null || !active.quill.isEnabled();
  group.controls.forEach(([, input]) => {
    const control = input as HTMLButtonElement | HTMLSelectElement;
    if (control.disabled !== disabled) control.disabled = disabled;
    if (input.tagName === 'SELECT') {
      const picker = getPicker(input);
      if (picker != null) picker.updateDisabled();
    }
  });
}

function observe(group: ToolbarGroup) {
  group.observer = new MutationObserver((mutations) => {
    let controlsChanged = false;
    mutations.forEach((mutation) => {
      if (mutation.type !== 'childList') return;
      if (!group.container.contains(mutation.target)) return;
      mutation.removedNodes.forEach((node) => {
        if (!(node instanceof HTMLElement)) return;
        const inputs = [node, ...Array.from(node.querySelectorAll('button'))];
        inputs.forEach((input) => {
          if (input.tagName !== 'BUTTON' || input.isConnected) return;
          const entry = group.listeners.get(input);
          if (entry == null) return;
          input.removeEventListener(entry.eventName, entry.listener);
          group.listeners.delete(input);
          const index = group.controls.findIndex(([, c]) => c === input);
          if (index >= 0) group.controls.splice(index, 1);
        });
      });
      mutation.addedNodes.forEach((node) => {
        if (!(node instanceof HTMLElement)) return;
        const inputs = [node, ...Array.from(node.querySelectorAll('button'))];
        inputs.forEach((input) => {
          if (input.tagName !== 'BUTTON') return;
          if (!group.container.contains(input)) return;
          if (group.listeners.has(input)) return;
          const toolbar =
            getActive(group) ?? Array.from(group.toolbars).find(isLive);
          if (toolbar == null) return;
          toolbar.attach(input);
          controlsChanged = true;
        });
      });
    });
    prune(group);
    syncDisabled(group);
    if (controlsChanged && group.active != null) {
      const [range] = group.active.quill.selection.getRange();
      group.active.update(range);
    }
  });
  group.observer.observe(document.body, {
    childList: true,
    subtree: true,
    attributes: true,
    attributeFilter: ['class'],
  });
}

function getActiveToolbar(container: HTMLElement) {
  const group = groups.get(container);
  return group == null ? null : getActive(group);
}

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

export { Toolbar as default, addControls, getActiveToolbar };
