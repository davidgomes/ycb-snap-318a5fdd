import Delta from 'quill-delta';
import { EmbedBlot, Scope } from 'parchment';
import Quill from '../core/quill.js';
import logger from '../core/logger.js';
import Module from '../core/module.js';
import type { Range } from '../core/selection.js';
import { getPicker } from '../ui/picker.js';

const debug = logger('quill:toolbar');

interface ToolbarGroup {
  container: HTMLElement;
  members: Set<Toolbar>;
  active: Toolbar | null;
  shared: boolean;
  bindings: Map<HTMLElement, { eventName: string; handler: EventListener }>;
  observer: MutationObserver;
}

const groups = new Map<HTMLElement, ToolbarGroup>();

const RESOLVE_KEY = '__quillToolbarResolve';

function containerOf(toolbar: { container?: HTMLElement | null }) {
  return toolbar.container instanceof HTMLElement ? toolbar.container : null;
}

function resolveActive(group: ToolbarGroup): Toolbar | null {
  Array.from(group.members).forEach((toolbar) => {
    if (!toolbar.quill.container.isConnected) {
      toolbar.release();
    }
  });
  if (!group.shared) {
    return group.members.values().next().value ?? null;
  }
  if (group.active != null && !group.members.has(group.active)) {
    group.active = null;
  }
  return group.active;
}

export function resolveToolbar(toolbar: {
  container?: HTMLElement | null;
}): Toolbar | null {
  const container = containerOf(toolbar);
  if (container == null) return null;
  const group = groups.get(container);
  if (group == null) return null;
  return resolveActive(group);
}

function installResolver(group: ToolbarGroup) {
  Object.defineProperty(group.container, RESOLVE_KEY, {
    configurable: true,
    value: () => resolveActive(group),
  });
}

let removalPatched = false;

function forEachToolbarInput(node: Node, visit: (input: HTMLElement) => void) {
  if (node instanceof DocumentFragment) {
    Array.from(node.childNodes).forEach((child) => {
      forEachToolbarInput(child, visit);
    });
    return;
  }
  collectInputs(node).forEach(visit);
}

function unbindDetached(node: Node) {
  forEachToolbarInput(node, (input) => {
    groups.forEach((group) => {
      if (group.bindings.has(input)) unbindControl(group, input);
    });
  });
}

function bindConnected(node: Node) {
  forEachToolbarInput(node, (input) => {
    if (!input.isConnected) return;
    groups.forEach((group) => {
      if (!group.container.contains(input)) return;
      const member = group.active ?? group.members.values().next().value;
      member?.attach(input);
    });
  });
}

function watchRemovals() {
  if (removalPatched) return;
  removalPatched = true;
  const originalRemove = Node.prototype.removeChild;
  Node.prototype.removeChild = function removeChildPatched<T extends Node>(
    this: Node,
    child: T,
  ): T {
    const removed = originalRemove.call(this, child) as T;
    if (!pruning) {
      pruning = true;
      try {
        unbindDetached(removed);
        groups.forEach((group) => resolveActive(group));
      } finally {
        pruning = false;
      }
    }
    return removed;
  };
  const originalAppend = Node.prototype.appendChild;
  Node.prototype.appendChild = function appendChildPatched<T extends Node>(
    this: Node,
    child: T,
  ): T {
    const added = originalAppend.call(this, child) as T;
    if (!pruning) bindConnected(added);
    return added;
  };
  const originalInsert = Node.prototype.insertBefore;
  Node.prototype.insertBefore = function insertBeforePatched<T extends Node>(
    this: Node,
    child: T,
    ref: Node | null,
  ): T {
    const added = originalInsert.call(this, child, ref) as T;
    if (!pruning) bindConnected(added);
    return added;
  };
}

let pruning = false;

function bindControl(group: ToolbarGroup, input: HTMLElement, format: string) {
  if (!group.bindings.has(input)) {
    const eventName = input.tagName === 'SELECT' ? 'change' : 'click';
    const handler: EventListener = (event) => {
      const toolbar = resolveActive(group);
      if (toolbar == null || !toolbar.quill.isEnabled()) {
        if (input.tagName !== 'SELECT') event.preventDefault();
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
      } else {
        if (input.classList.contains('ql-active')) {
          value = false;
        } else {
          const button = input as HTMLButtonElement;
          value = button.value || !input.hasAttribute('value');
        }
        event.preventDefault();
      }
      toolbar.quill.focus();
      const [range] = toolbar.quill.selection.getRange();
      if (toolbar.handlers[format] != null) {
        toolbar.handlers[format].call(toolbar, value);
      } else if (
        // @ts-expect-error
        toolbar.quill.scroll.query(format).prototype instanceof EmbedBlot
      ) {
        value = prompt(`Enter ${format}`); // eslint-disable-line no-alert
        if (!value) return;
        toolbar.quill.updateContents(
          new Delta()
            // @ts-expect-error Fix me later
            .retain(range.index)
            // @ts-expect-error Fix me later
            .delete(range.length)
            .insert({ [format]: value }),
          Quill.sources.USER,
        );
      } else {
        toolbar.quill.format(format, value, Quill.sources.USER);
      }
      toolbar.update(range);
    };
    input.addEventListener(eventName, handler);
    group.bindings.set(input, { eventName, handler });
  }
  group.members.forEach((toolbar) => {
    if (!toolbar.controls.some((pair) => pair[1] === input)) {
      toolbar.controls.push([format, input]);
    }
  });
}

function unbindControl(group: ToolbarGroup, input: HTMLElement) {
  const binding = group.bindings.get(input);
  if (binding) {
    input.removeEventListener(binding.eventName, binding.handler);
    group.bindings.delete(input);
  }
  group.members.forEach((toolbar) => {
    toolbar.controls = toolbar.controls.filter((pair) => pair[1] !== input);
  });
}

function collectInputs(node: Node): HTMLElement[] {
  if (!(node instanceof HTMLElement)) return [];
  const inputs: HTMLElement[] = [];
  if (node.tagName === 'BUTTON' || node.tagName === 'SELECT') {
    inputs.push(node);
  }
  node.querySelectorAll('button, select').forEach((input) => {
    if (input instanceof HTMLElement) inputs.push(input);
  });
  return inputs;
}

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
    const group = this.ensureGroup();
    Array.from(this.container.querySelectorAll('button, select')).forEach(
      (input) => {
        if (input instanceof HTMLElement) this.attach(input);
      },
    );
    this.onEditorChange = () => {
      if (!this.isToolbarActive()) return;
      const [range] = this.quill.selection.getRange(); // quill.getSelection triggers update
      this.update(range);
    };
    this.onSelectionChange = (range: Range | null) => {
      if (!this.quill.container.isConnected) {
        this.release();
        return;
      }
      if (range != null) this.makeActive();
    };
    this.onFocus = () => {
      if (!this.quill.container.isConnected) {
        this.release();
        return;
      }
      this.makeActive();
    };
    this.quill.on(Quill.events.EDITOR_CHANGE, this.onEditorChange);
    this.quill.on(Quill.events.SELECTION_CHANGE, this.onSelectionChange);
    this.quill.root.addEventListener('focusin', this.onFocus);
    if (group.shared && this.quill.hasFocus()) this.makeActive();
    this.refreshDisabled();
  }

  private onEditorChange: () => void = () => {};

  private onSelectionChange: (range: Range | null) => void = () => {};

  private onFocus: () => void = () => {};

  private released = false;

  private ensureGroup() {
    const container = this.container as HTMLElement;
    watchRemovals();
    let group = groups.get(container);
    if (group == null) {
      group = {
        container,
        members: new Set(),
        active: null,
        shared: false,
        bindings: new Map(),
        observer: new MutationObserver((mutations) => {
          mutations.forEach((mutation) => {
            mutation.removedNodes.forEach((node) => {
              collectInputs(node).forEach((input) => {
                unbindControl(group as ToolbarGroup, input);
              });
            });
            mutation.addedNodes.forEach((node) => {
              collectInputs(node).forEach((input) => {
                const member =
                  (group as ToolbarGroup).active ??
                  (group as ToolbarGroup).members.values().next().value;
                member?.attach(input);
              });
            });
          });
        }),
      };
      group.observer.observe(container, { childList: true, subtree: true });
      groups.set(container, group);
      installResolver(group);
    }
    group.members.add(this);
    if (group.members.size > 1) group.shared = true;
    return group;
  }

  private isToolbarActive() {
    const container = containerOf(this);
    if (container == null) return false;
    const group = groups.get(container);
    if (group == null || !group.shared) return true;
    return group.active === this;
  }

  private makeActive() {
    const container = containerOf(this);
    if (container == null || this.released) return;
    const group = groups.get(container);
    if (group == null) return;
    group.active = this;
    const [range] = this.quill.selection.getRange();
    this.update(range);
    this.refreshDisabled();
    this.syncImageInput();
  }

  refreshDisabled() {
    const container = containerOf(this);
    if (container == null) return;
    const group = groups.get(container);
    if (group == null || !group.shared || group.active !== this) return;
    const disabled = !this.quill.isEnabled();
    this.controls.forEach(([, input]) => {
      if (
        input instanceof HTMLButtonElement ||
        input instanceof HTMLSelectElement
      ) {
        input.disabled = disabled;
      }
    });
    container.querySelectorAll('.ql-picker').forEach((picker) => {
      if (!(picker instanceof HTMLElement)) return;
      picker.classList.toggle('ql-disabled', disabled);
      picker.setAttribute('aria-disabled', disabled ? 'true' : 'false');
      const label = picker.querySelector('.ql-picker-label');
      if (label instanceof HTMLElement) {
        label.setAttribute('aria-disabled', disabled ? 'true' : 'false');
      }
      if (disabled) picker.classList.remove('ql-expanded');
    });
    const fileInput = container.querySelector('input.ql-image[type=file]');
    if (fileInput instanceof HTMLInputElement) {
      fileInput.disabled = disabled;
    }
  }

  private syncImageInput() {
    const container = containerOf(this);
    if (container == null) return;
    const fileInput = container.querySelector('input.ql-image[type=file]');
    if (!(fileInput instanceof HTMLInputElement)) return;
    const uploader = this.quill.uploader as unknown as {
      options?: { mimetypes?: string[] };
    };
    const mimetypes = uploader?.options?.mimetypes;
    if (mimetypes != null) {
      fileInput.setAttribute('accept', mimetypes.join(', '));
    }
    fileInput.disabled = !this.quill.isEnabled();
  }

  release() {
    if (this.released) return;
    this.released = true;
    const container = containerOf(this);
    this.quill.off(Quill.events.EDITOR_CHANGE, this.onEditorChange);
    this.quill.off(Quill.events.SELECTION_CHANGE, this.onSelectionChange);
    this.quill.root.removeEventListener('focusin', this.onFocus);
    if (container == null) return;
    const group = groups.get(container);
    if (group == null) return;
    group.members.delete(this);
    if (group.active === this) {
      group.active = null;
      this.controls.forEach(([, input]) => {
        input.classList.remove('ql-active');
        if (input.tagName === 'BUTTON') {
          input.setAttribute('aria-pressed', 'false');
        }
        if (
          input instanceof HTMLButtonElement ||
          input instanceof HTMLSelectElement
        ) {
          input.disabled = false;
        }
      });
      container.querySelectorAll('.ql-picker').forEach((picker) => {
        if (!(picker instanceof HTMLElement)) return;
        picker.classList.remove('ql-disabled', 'ql-expanded');
        picker.setAttribute('aria-disabled', 'false');
        const label = picker.querySelector('.ql-picker-label');
        if (label instanceof HTMLElement) {
          label.classList.remove('ql-active');
          label.setAttribute('aria-disabled', 'false');
        }
      });
      const fileInput = container.querySelector('input.ql-image[type=file]');
      if (fileInput instanceof HTMLInputElement) {
        if (group.members.size === 0) {
          fileInput.remove();
        } else {
          fileInput.disabled = true;
          fileInput.value = '';
        }
      }
    }
    if (group.members.size === 0) {
      group.bindings.forEach(({ eventName, handler }, input) => {
        input.removeEventListener(eventName, handler);
      });
      group.bindings.clear();
      group.observer.disconnect();
      groups.delete(container);
    }
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
    const container = containerOf(this);
    const group = container != null ? groups.get(container) : undefined;
    if (group == null) return;
    const supported = Array.from(group.members).some(
      (toolbar) =>
        toolbar.handlers[format] != null ||
        toolbar.quill.scroll.query(format) != null,
    );
    if (!supported) {
      debug.warn('ignoring attaching to nonexistent format', format, input);
      return;
    }
    bindControl(group, input, format);
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
        if (input instanceof HTMLSelectElement) {
          getPicker(input)?.update();
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
