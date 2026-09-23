import Delta from 'quill-delta';
import { EmbedBlot, Scope } from 'parchment';
import Quill from '../core/quill.js';
import logger from '../core/logger.js';
import Module from '../core/module.js';
import type { Range } from '../core/selection.js';
import { pickerFromContainer } from '../ui/picker.js';

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

type ControlBinding = {
  type: string;
  listener: EventListener;
  format: string;
};

const toolbarGroups = new WeakMap<HTMLElement, ToolbarGroup>();
const removalHooks = new Map<Node, () => void>();
let removalObserver: MutationObserver | null = null;
let domHooksInstalled = false;

class ToolbarGroup {
  readonly controls: [string, HTMLElement][] = [];

  private readonly members = new Set<Toolbar>();
  private readonly bindings = new Map<HTMLElement, ControlBinding>();
  private readonly connected = new Set<Toolbar>();
  private readonly skipped = new WeakSet<HTMLElement>();
  private readonly observer: MutationObserver;

  private active: Toolbar | null = null;
  private suppressImplicit = false;
  private forgetting = false;
  private closed = false;

  constructor(private readonly container: HTMLElement) {
    this.observer = new MutationObserver((records) => {
      records.forEach((record) => {
        record.addedNodes.forEach((node) => {
          this.bindTree(node);
        });
      });
    });
    this.observer.observe(container, { childList: true, subtree: true });
    ensureRemovalObserver();
  }

  add(toolbar: Toolbar) {
    this.members.add(toolbar);
    if (toolbar.quill.container.isConnected) {
      this.connected.add(toolbar);
    }
    this.scan();
    watchRemoval(toolbar.quill.container, () => this.forget(toolbar));
    watchRemoval(toolbar.quill.root, () => this.forget(toolbar));
  }

  activate(toolbar: Toolbar, range?: Range | null) {
    if (this.closed || this.forgetting || !this.members.has(toolbar)) return;
    if (this.isDetached(toolbar)) {
      this.forget(toolbar);
      return;
    }
    this.active = toolbar;
    const next =
      range === undefined ? toolbar.quill.selection.getRange()[0] : range;
    toolbar.update(next);
    this.syncDisabled();
    this.syncFileInput();
    this.refreshPickers();
  }

  forget(toolbar: Toolbar) {
    if (this.closed || this.forgetting || !this.members.has(toolbar)) return;
    this.forgetting = true;
    try {
      const onlyMember = this.members.size === 1;
      const wasTarget =
        this.active === toolbar ||
        (this.active == null && !this.suppressImplicit && onlyMember);
      this.members.delete(toolbar);
      this.connected.delete(toolbar);
      removalHooks.delete(toolbar.quill.container);
      removalHooks.delete(toolbar.quill.root);
      if (wasTarget) {
        this.active = null;
        this.suppressImplicit = this.members.size > 0;
        toolbar.update(null);
        this.refreshPickers();
        this.removeFileInputs();
        this.syncDisabled();
      }
      toolbar.releaseSharedListeners();
      if (this.members.size === 0) {
        this.teardown();
      }
    } finally {
      this.forgetting = false;
    }
  }

  resolveTarget(): Toolbar | null {
    if (this.closed) return null;
    if (this.active != null) {
      if (!this.members.has(this.active) || this.isDetached(this.active)) {
        const stale = this.active;
        if (this.members.has(stale)) this.forget(stale);
        else this.active = null;
        return this.resolveTarget();
      }
      return this.active;
    }
    if (this.suppressImplicit || this.forgetting) return null;
    if (this.members.size === 1) {
      const only = this.members.values().next().value as Toolbar | undefined;
      if (only != null && !this.isDetached(only)) return only;
    }
    return null;
  }

  isShared(): boolean {
    return this.members.size > 1;
  }

  syncDisabled() {
    const target = this.forgetting ? null : this.resolveTarget();
    const disabled = target != null && !target.quill.isEnabled();
    this.container.querySelectorAll('button, select').forEach((node) => {
      if (
        node instanceof HTMLButtonElement ||
        node instanceof HTMLSelectElement
      ) {
        node.disabled = disabled;
      }
    });
    this.container.querySelectorAll('.ql-picker').forEach((node) => {
      if (node instanceof HTMLElement) {
        pickerFromContainer(node)?.setDisabled(disabled);
      }
    });
  }

  syncFileInput() {
    const inputs = Array.from(
      this.container.querySelectorAll<HTMLInputElement>(
        'input.ql-image[type=file]',
      ),
    );
    inputs.slice(1).forEach((input) => input.remove());
    const input = inputs[0];
    const target = this.forgetting ? null : this.resolveTarget();
    if (input == null || target == null) return;
    const mimetypes = uploaderMimetypes(target.quill);
    if (mimetypes != null) {
      input.setAttribute('accept', mimetypes.join(', '));
    }
  }

  refreshPickers() {
    this.container.querySelectorAll('.ql-picker').forEach((node) => {
      if (node instanceof HTMLElement) {
        pickerFromContainer(node)?.update();
      }
    });
  }

  removeFileInputs() {
    this.container
      .querySelectorAll('input.ql-image[type=file]')
      .forEach((input) => input.remove());
  }

  private scan() {
    this.container.querySelectorAll('button, select').forEach((node) => {
      if (node instanceof HTMLElement) this.bindControl(node);
    });
  }

  bindTree(node: Node) {
    if (!(node instanceof Element)) return;
    const controls: HTMLElement[] = [];
    if (
      node instanceof HTMLElement &&
      (node.tagName === 'BUTTON' || node.tagName === 'SELECT')
    ) {
      controls.push(node);
    }
    node.querySelectorAll('button, select').forEach((control) => {
      if (control instanceof HTMLElement) controls.push(control);
    });
    let changed = false;
    controls.forEach((control) => {
      if (
        control.closest('.ql-picker') != null &&
        control.tagName !== 'SELECT'
      ) {
        return;
      }
      const before = this.bindings.size;
      this.bindControl(control);
      if (this.bindings.size > before || this.bindings.has(control)) {
        changed = true;
      }
    });
    if (!changed) return;
    const target = this.resolveTarget();
    if (target == null) return;
    const [range] = target.quill.selection.getRange();
    target.update(range);
    this.syncDisabled();
  }

  private bindControl(input: HTMLElement) {
    if (this.bindings.has(input)) return;
    const format = controlFormat(input);
    if (format == null) return;
    if (!this.memberSupports(format)) {
      if (!this.skipped.has(input)) {
        this.skipped.add(input);
        debug.warn('ignoring attaching to nonexistent format', format, input);
      }
      return;
    }
    if (input.tagName === 'BUTTON') {
      input.setAttribute('type', 'button');
    }
    const eventName = input.tagName === 'SELECT' ? 'change' : 'click';
    const listener: EventListener = (event) => {
      this.onControlEvent(input, format, event);
    };
    input.addEventListener(eventName, listener);
    this.bindings.set(input, { type: eventName, listener, format });
    this.controls.push([format, input]);
    const target = this.forgetting ? null : this.resolveTarget();
    if (
      input instanceof HTMLButtonElement ||
      input instanceof HTMLSelectElement
    ) {
      input.disabled = target != null && !target.quill.isEnabled();
    }
  }

  private onControlEvent(input: HTMLElement, format: string, event: Event) {
    const target = this.resolveTarget();
    if (target == null) {
      event.preventDefault();
      const member = this.members.values().next().value as Toolbar | undefined;
      member?.update(null);
      this.refreshPickers();
      return;
    }
    if (!target.quill.isEnabled()) {
      event.preventDefault();
      const [range] = target.quill.selection.getRange();
      target.update(range);
      this.refreshPickers();
      return;
    }
    target.applyControl(input, format, event);
  }

  private memberSupports(format: string) {
    for (const toolbar of this.members) {
      if (toolbar.handles(format)) return true;
    }
    return false;
  }

  private isDetached(toolbar: Toolbar) {
    if (!this.connected.has(toolbar)) {
      if (toolbar.quill.container.isConnected) this.connected.add(toolbar);
      return false;
    }
    return (
      !toolbar.quill.container.isConnected || !toolbar.quill.root.isConnected
    );
  }

  private teardown() {
    if (this.closed) return;
    this.closed = true;
    this.observer.disconnect();
    this.bindings.forEach((binding, input) => {
      input.removeEventListener(binding.type, binding.listener);
    });
    this.bindings.clear();
    this.controls.length = 0;
    this.active = null;
    if (toolbarGroups.get(this.container) === this) {
      toolbarGroups.delete(this.container);
    }
  }
}

function controlFormat(input: HTMLElement): string | null {
  const className = Array.from(input.classList).find((name) => {
    return name.indexOf('ql-') === 0;
  });
  if (className == null) return null;
  return className.slice('ql-'.length);
}

function uploaderMimetypes(quill: Quill): string[] | null {
  const uploader = quill.uploader as unknown as {
    options?: { mimetypes?: string[] };
  };
  return uploader.options?.mimetypes ?? null;
}

function toolbarAncestor(node: Node | null): HTMLElement | null {
  let current: Node | null = node;
  while (current != null) {
    if (current instanceof HTMLElement && toolbarGroups.has(current)) {
      return current;
    }
    current = current.parentNode;
  }
  return null;
}

function noteInsertion(parent: Node, child: Node) {
  if (!(parent instanceof Element) || parent.closest('.ql-editor') != null) {
    return;
  }
  const toolbar = toolbarAncestor(parent);
  if (toolbar == null) return;
  toolbarGroups.get(toolbar)?.bindTree(child);
}

function watchRemoval(node: Node, hook: () => void) {
  removalHooks.set(node, hook);
  ensureRemovalObserver();
}

function notifyRemovedTree(node: Node) {
  const found: Node[] = [];
  const consider = (current: Node) => {
    if (removalHooks.has(current)) found.push(current);
  };
  consider(node);
  if (node instanceof Element) {
    node.querySelectorAll('.ql-container, .ql-editor').forEach(consider);
  }
  found.forEach((current) => {
    const hook = removalHooks.get(current);
    removalHooks.delete(current);
    if (hook == null) return;
    try {
      hook();
    } catch (error) {
      debug.error(error);
    }
  });
}

function ensureRemovalObserver() {
  if (removalObserver != null || typeof document === 'undefined') return;
  removalObserver = new MutationObserver(() => {
    const pending: Node[] = [];
    for (const [node] of removalHooks) {
      if (!node.isConnected) pending.push(node);
    }
    pending.forEach((node) => {
      const hook = removalHooks.get(node);
      removalHooks.delete(node);
      if (hook == null) return;
      try {
        hook();
      } catch (error) {
        debug.error(error);
      }
    });
  });
  removalObserver.observe(document.body, { childList: true, subtree: true });
}

function installDomHooks() {
  if (domHooksInstalled || typeof Node === 'undefined') return;
  domHooksInstalled = true;
  const originalRemoveChild = Node.prototype.removeChild;
  Node.prototype.removeChild = function removeChild<T extends Node>(
    this: Node,
    child: T,
  ): T {
    const removed = originalRemoveChild.call(this, child) as T;
    notifyRemovedTree(removed);
    return removed;
  };
  const originalAppendChild = Node.prototype.appendChild;
  Node.prototype.appendChild = function appendChild<T extends Node>(
    this: Node,
    child: T,
  ): T {
    const inserted = originalAppendChild.call(this, child) as T;
    noteInsertion(this, inserted);
    return inserted;
  };
  const originalInsertBefore = Node.prototype.insertBefore;
  Node.prototype.insertBefore = function insertBefore<T extends Node>(
    this: Node,
    child: T,
    ref: Node | null,
  ): T {
    const inserted = originalInsertBefore.call(this, child, ref) as T;
    noteInsertion(this, inserted);
    return inserted;
  };
  const originalAppend = Element.prototype.append;
  Element.prototype.append = function append(
    ...nodes: Array<Node | string>
  ): void {
    originalAppend.apply(this, nodes);
    nodes.forEach((node) => {
      if (node instanceof Node) noteInsertion(this, node);
    });
  };
  // Element.remove() uses the internal DOM removal path and does not call
  // the patched Node.prototype.removeChild.
  const originalRemove = Element.prototype.remove;
  Element.prototype.remove = function remove(this: Element) {
    originalRemove.call(this);
    notifyRemovedTree(this);
  };
}

installDomHooks();

function activeToolbarQuill(container?: HTMLElement | null): Quill | null {
  if (container == null) return null;
  return toolbarGroups.get(container)?.resolveTarget()?.quill ?? null;
}

class Toolbar extends Module<ToolbarProps> {
  static DEFAULTS: ToolbarProps;

  container?: HTMLElement | null;
  controls: [string, HTMLElement][];
  handlers: Record<string, Handler>;

  private group?: ToolbarGroup;
  private originalEnable?: Quill['enable'];
  private readonly onEditorChange = (type: string, range?: Range | null) => {
    if (this.group == null) return;
    if (!this.quill.container.isConnected || !this.quill.root.isConnected) {
      this.group.forget(this);
      return;
    }
    if (type === Quill.events.SELECTION_CHANGE && range != null) {
      this.group.activate(this, range);
      return;
    }
    if (this.group.resolveTarget() !== this) return;
    const [current] = this.quill.selection.getRange(); // quill.getSelection triggers update
    this.update(current);
  };

  private readonly onFocus = () => {
    this.group?.activate(this);
  };

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
      this.controls = [];
      this.handlers = {};
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
    let group = toolbarGroups.get(this.container);
    if (group == null) {
      group = new ToolbarGroup(this.container);
      toolbarGroups.set(this.container, group);
    }
    this.group = group;
    this.controls = group.controls;
    group.add(this);
    this.quill.on(Quill.events.EDITOR_CHANGE, this.onEditorChange);
    this.quill.root.addEventListener('focus', this.onFocus);
    this.installEnableHook();
  }

  addHandler(format: string, handler: Handler) {
    this.handlers[format] = handler;
  }

  handles(format: string): boolean {
    return (
      this.handlers[format] != null || this.quill.scroll.query(format) != null
    );
  }

  isShared(): boolean {
    return this.group?.isShared() ?? false;
  }

  shouldUpdate(): boolean {
    return this.group?.resolveTarget() === this;
  }

  syncSharedControls() {
    if (this.group == null) return;
    this.group.syncDisabled();
    if (!this.shouldUpdate()) return;
    const [range] = this.quill.selection.getRange();
    this.update(range);
    this.group.refreshPickers();
  }

  attach(input: HTMLElement) {
    this.group?.bindTree(input);
  }

  applyControl(input: HTMLElement, format: string, event: Event) {
    if (!this.handles(format)) {
      debug.warn('ignoring attaching to nonexistent format', format, input);
      return;
    }
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
      event.preventDefault();
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

  releaseSharedListeners() {
    this.quill.off(Quill.events.EDITOR_CHANGE, this.onEditorChange);
    this.quill.root.removeEventListener('focus', this.onFocus);
    if (this.originalEnable != null) {
      this.quill.enable = this.originalEnable;
      this.originalEnable = undefined;
    }
  }

  private installEnableHook() {
    const group = this.group;
    if (group == null || this.originalEnable != null) return;
    this.originalEnable = this.quill.enable;
    this.quill.enable = (enabled = true) => {
      this.originalEnable?.call(this.quill, enabled);
      if (group.resolveTarget() !== this) return;
      group.syncDisabled();
      const [range] = this.quill.selection.getRange();
      this.update(range);
      group.refreshPickers();
    };
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

export { Toolbar as default, addControls, activeToolbarQuill };
