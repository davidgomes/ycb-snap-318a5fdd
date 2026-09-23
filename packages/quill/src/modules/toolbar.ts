import Delta from 'quill-delta';
import { EmbedBlot, Scope } from 'parchment';
import Quill from '../core/quill.js';
import logger from '../core/logger.js';
import Module from '../core/module.js';
import type { Range } from '../core/selection.js';
import { forgetPicker, getPicker } from '../ui/picker.js';

const debug = logger('quill:toolbar');

type ControlBinding = {
  format: string;
  eventName: 'click' | 'change';
  listener: EventListener;
  mouseListener: EventListener | null;
};

const imageListenerByInput = new WeakMap<HTMLInputElement, EventListener>();
const editorToolbarsByContainer = new WeakMap<HTMLElement, Toolbar>();
const editorToolbarsByRoot = new WeakMap<HTMLElement, Toolbar>();
const liveToolbars = new Set<Toolbar>();

let domHooksInstalled = false;
let domHookDepth = 0;
let liveObserver: MutationObserver | null = null;

function formatOf(input: Element) {
  const className = Array.from(input.classList).find((name) => {
    return name.indexOf('ql-') === 0;
  });
  if (className == null) return null;
  return className.slice('ql-'.length);
}

function installDomHooks() {
  if (domHooksInstalled || typeof Node === 'undefined') return;
  domHooksInstalled = true;

  const originalAppendChild = Node.prototype.appendChild;
  Node.prototype.appendChild = function appendChild<T extends Node>(
    this: Node,
    node: T,
  ): T {
    const result = originalAppendChild.call(this, node) as T;
    notifyToolbarMutation(this);
    return result;
  };

  const originalInsertBefore = Node.prototype.insertBefore;
  Node.prototype.insertBefore = function insertBefore<T extends Node>(
    this: Node,
    node: T,
    child: Node | null,
  ): T {
    const result = originalInsertBefore.call(this, node, child) as T;
    notifyToolbarMutation(this);
    return result;
  };

  const originalRemoveChild = Node.prototype.removeChild;
  Node.prototype.removeChild = function removeChild<T extends Node>(
    this: Node,
    child: T,
  ): T {
    const removed = originalRemoveChild.call(this, child) as T;
    notifyRemoval(removed);
    notifyToolbarMutation(this);
    return removed;
  };

  const originalReplaceChild = Node.prototype.replaceChild;
  Node.prototype.replaceChild = function replaceChild<T extends Node>(
    this: Node,
    node: Node,
    child: T,
  ): T {
    const removed = originalReplaceChild.call(this, node, child) as T;
    notifyRemoval(removed);
    notifyToolbarMutation(this);
    return removed;
  };

  const originalRemove = Element.prototype.remove;
  Element.prototype.remove = function remove(this: Element) {
    const parent = this.parentNode;
    originalRemove.call(this);
    notifyRemoval(this);
    if (parent != null) notifyToolbarMutation(parent);
  };

  const originalAppend = Element.prototype.append;
  Element.prototype.append = function append(
    this: Element,
    ...nodes: (Node | string)[]
  ) {
    originalAppend.apply(this, nodes);
    notifyToolbarMutation(this);
  };

  const originalPrepend = Element.prototype.prepend;
  Element.prototype.prepend = function prepend(
    this: Element,
    ...nodes: (Node | string)[]
  ) {
    originalPrepend.apply(this, nodes);
    notifyToolbarMutation(this);
  };
}

function ensureLiveObserver() {
  if (liveObserver != null || typeof MutationObserver === 'undefined') return;
  if (typeof document === 'undefined') return;
  liveObserver = new MutationObserver(() => {
    if (domHookDepth > 0) return;
    Array.from(liveToolbars).forEach((toolbar) => {
      if (!toolbar.released && toolbar.needsRelease()) toolbar.release();
    });
  });
  liveObserver.observe(document.documentElement, {
    childList: true,
    subtree: true,
  });
}

function eachEditorToolbar(node: Node, visit: (toolbar: Toolbar) => void) {
  if (!(node instanceof Element)) return;
  const seen = new Set<Toolbar>();
  const visitElement = (element: Element) => {
    if (!(element instanceof HTMLElement)) return;
    const toolbar =
      editorToolbarsByContainer.get(element) ??
      editorToolbarsByRoot.get(element);
    if (toolbar != null && !seen.has(toolbar)) {
      seen.add(toolbar);
      visit(toolbar);
    }
  };
  visitElement(node);
  if (node.childElementCount === 0) return;
  if (node.querySelector('.ql-container, .ql-editor') == null) return;
  node.querySelectorAll('.ql-container, .ql-editor').forEach((element) => {
    visitElement(element);
  });
}

function notifyRemoval(node: Node) {
  eachEditorToolbar(node, (toolbar) => {
    if (toolbar.needsRelease()) toolbar.release();
  });
}

function notifyToolbarMutation(node: Node) {
  if (domHookDepth > 0) return;
  const container = findToolbarContainer(node);
  if (container == null) return;
  const group = ToolbarGroup.lookup(container);
  if (group == null || group.destroyed) return;
  domHookDepth += 1;
  try {
    group.syncFromDom();
  } finally {
    domHookDepth -= 1;
  }
}

function findToolbarContainer(node: Node | null) {
  let current: Node | null = node;
  while (current != null) {
    if (
      current instanceof HTMLElement &&
      ToolbarGroup.lookup(current) != null
    ) {
      return current;
    }
    current = current.parentNode;
  }
  return null;
}

export function getActiveToolbar(container: HTMLElement | null | undefined) {
  if (container == null) return null;
  return ToolbarGroup.lookup(container)?.current() ?? null;
}

export function bindSharedImageInput(
  container: HTMLElement,
  input: HTMLInputElement,
) {
  const previous = imageListenerByInput.get(input);
  if (previous != null) input.removeEventListener('change', previous);
  const listener = () => {
    const toolbar = getActiveToolbar(container);
    if (toolbar == null || !toolbar.quill.isEnabled()) {
      input.value = '';
      return;
    }
    const range = toolbar.quill.getSelection(true);
    if (range == null || input.files == null) {
      input.value = '';
      return;
    }
    toolbar.quill.uploader.upload(range, input.files);
    input.value = '';
  };
  input.addEventListener('change', listener);
  imageListenerByInput.set(input, listener);
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

class ToolbarGroup {
  static groups = new WeakMap<HTMLElement, ToolbarGroup>();

  container: HTMLElement;
  members = new Set<Toolbar>();
  active: Toolbar | null = null;
  shared = false;
  destroyed = false;
  bindings = new Map<HTMLElement, ControlBinding>();
  observer: MutationObserver | null = null;

  static lookup(container: HTMLElement | null | undefined) {
    if (container == null) return null;
    return ToolbarGroup.groups.get(container) ?? null;
  }

  static forContainer(container: HTMLElement) {
    let group = ToolbarGroup.groups.get(container);
    if (group == null || group.destroyed) {
      group = new ToolbarGroup(container);
      ToolbarGroup.groups.set(container, group);
    }
    return group;
  }

  constructor(container: HTMLElement) {
    this.container = container;
    installDomHooks();
    ensureLiveObserver();
    const innerHTML = Object.getOwnPropertyDescriptor(
      Element.prototype,
      'innerHTML',
    );
    if (innerHTML?.set != null && innerHTML.get != null) {
      const container = this.container;
      const sync = () => {
        if (!this.destroyed && domHookDepth === 0) this.syncFromDom();
      };
      Object.defineProperty(container, 'innerHTML', {
        configurable: true,
        enumerable: true,
        get() {
          return innerHTML.get?.call(container);
        },
        set(value: string) {
          innerHTML.set?.call(container, value);
          sync();
        },
      });
    }
    if (typeof MutationObserver !== 'undefined') {
      this.observer = new MutationObserver(() => {
        if (this.destroyed || domHookDepth > 0) return;
        this.syncFromDom();
      });
      this.observer.observe(this.container, {
        childList: true,
        subtree: true,
      });
    }
  }

  add(toolbar: Toolbar) {
    this.members.add(toolbar);
    if (this.members.size > 1) this.shared = true;
    this.syncFromDom();
    this.syncDisabled();
  }

  remove(toolbar: Toolbar) {
    const wasActive = this.active === toolbar;
    this.members.delete(toolbar);
    if (this.active === toolbar) this.active = null;
    if (this.members.size === 0) {
      this.destroy();
      return;
    }
    if (wasActive) {
      this.clearActiveState();
      this.removeImageInputs();
    }
    this.syncDisabled();
  }

  current() {
    Array.from(this.members).forEach((toolbar) => {
      if (!toolbar.released && toolbar.needsRelease()) toolbar.release();
    });
    if (
      this.active != null &&
      this.members.has(this.active) &&
      !this.active.released
    ) {
      return this.active;
    }
    if (!this.shared) {
      for (const toolbar of this.members) {
        if (!toolbar.released) return toolbar;
      }
    }
    return null;
  }

  markActive(toolbar: Toolbar) {
    if (toolbar.released || !this.members.has(toolbar)) return;
    const changed = this.active !== toolbar;
    this.active = toolbar;
    if (changed) this.closePickers();
    this.syncDisabled();
    this.syncImageInput();
  }

  allows(format: string) {
    for (const toolbar of this.members) {
      if (toolbar.handlers[format] != null) return true;
      if (toolbar.quill.scroll.query(format) != null) return true;
    }
    return false;
  }

  syncDisabled() {
    const toolbar = this.current();
    if (toolbar == null) {
      if (this.shared) this.setControlsDisabled(false);
      return;
    }
    this.setControlsDisabled(!toolbar.quill.isEnabled());
  }

  setControlsDisabled(disabled: boolean) {
    this.container.querySelectorAll('button, select').forEach((node) => {
      if (
        node instanceof HTMLButtonElement ||
        node instanceof HTMLSelectElement
      ) {
        node.disabled = disabled;
      }
      if (node instanceof HTMLSelectElement) {
        getPicker(node)?.reflectDisabled();
      }
    });
  }

  closePickers() {
    this.container.querySelectorAll('select').forEach((node) => {
      if (node instanceof HTMLSelectElement) getPicker(node)?.close();
    });
  }

  syncImageInput() {
    const toolbar = this.current();
    const input = this.container.querySelector('input.ql-image[type=file]');
    if (!(input instanceof HTMLInputElement) || toolbar == null) return;
    input.setAttribute('accept', toolbar.fileAccept());
    bindSharedImageInput(this.container, input);
  }

  clearActiveState() {
    this.container.querySelectorAll('button').forEach((node) => {
      node.classList.remove('ql-active');
      node.setAttribute('aria-pressed', 'false');
    });
    this.container.querySelectorAll('select').forEach((node) => {
      if (!(node instanceof HTMLSelectElement)) return;
      node.value = '';
      node.selectedIndex = -1;
      getPicker(node)?.update();
    });
  }

  removeImageInputs() {
    domHookDepth += 1;
    try {
      this.container
        .querySelectorAll('input.ql-image[type=file]')
        .forEach((node) => {
          if (node instanceof HTMLInputElement) {
            const listener = imageListenerByInput.get(node);
            if (listener != null) {
              node.removeEventListener('change', listener);
              imageListenerByInput.delete(node);
            }
          }
          node.remove();
        });
    } finally {
      domHookDepth -= 1;
    }
  }

  syncFromDom() {
    const seen = new Set<HTMLElement>();
    this.container.querySelectorAll('button, select').forEach((node) => {
      if (!(node instanceof HTMLElement)) return;
      if (node.tagName === 'BUTTON') node.setAttribute('type', 'button');
      seen.add(node);
      this.bind(node, false);
    });
    Array.from(this.bindings.keys()).forEach((input) => {
      if (!seen.has(input) || !this.container.contains(input)) {
        this.unbind(input);
      }
    });
    this.syncDisabled();
  }

  bind(input: HTMLElement, warn: boolean) {
    const format = formatOf(input);
    if (format == null) return;
    if (this.bindings.has(input)) {
      this.remember(input);
      return;
    }
    if (!this.allows(format)) {
      if (warn) {
        debug.warn('ignoring attaching to nonexistent format', format, input);
      }
      return;
    }
    const eventName = input.tagName === 'SELECT' ? 'change' : 'click';
    const listener: EventListener = (event) => {
      this.handleControlEvent(input, format, event);
    };
    let mouseListener: EventListener | null = null;
    if (eventName === 'click') {
      mouseListener = (event) => {
        event.preventDefault();
      };
      input.addEventListener('mousedown', mouseListener);
    }
    input.addEventListener(eventName, listener);
    this.bindings.set(input, { format, eventName, listener, mouseListener });
    this.remember(input);
  }

  handleControlEvent(input: HTMLElement, format: string, event: Event) {
    if (
      (input instanceof HTMLButtonElement ||
        input instanceof HTMLSelectElement) &&
      input.disabled
    ) {
      event.preventDefault();
      return;
    }
    const toolbar = this.current();
    if (toolbar == null || !toolbar.quill.isEnabled()) {
      event.preventDefault();
      return;
    }
    let value: string | boolean | null | undefined;
    if (input.tagName === 'SELECT') {
      if (!(input instanceof HTMLSelectElement)) return;
      if (input.selectedIndex < 0) return;
      const selected = input.options[input.selectedIndex];
      if (selected == null) return;
      if (selected.hasAttribute('selected')) {
        value = false;
      } else {
        value = selected.value || false;
      }
    } else {
      if (input.classList.contains('ql-active')) {
        value = false;
      } else {
        value =
          (input instanceof HTMLButtonElement ? input.value : '') ||
          !input.hasAttribute('value');
      }
      event.preventDefault();
    }
    toolbar.quill.focus();
    const [range] = toolbar.quill.selection.getRange();
    if (toolbar.handlers[format] != null) {
      toolbar.handlers[format].call(toolbar, value);
    } else {
      const blot = toolbar.quill.scroll.query(format);
      if (typeof blot === 'function' && blot.prototype instanceof EmbedBlot) {
        const entered = prompt(`Enter ${format}`); // eslint-disable-line no-alert
        if (!entered || range == null) return;
        toolbar.quill.updateContents(
          new Delta()
            .retain(range.index)
            .delete(range.length)
            .insert({ [format]: entered }),
          Quill.sources.USER,
        );
      } else {
        toolbar.quill.format(format, value, Quill.sources.USER);
      }
    }
    toolbar.update(range);
  }

  remember(input: HTMLElement) {
    const binding = this.bindings.get(input);
    if (binding == null) return;
    this.members.forEach((toolbar) => {
      toolbar.rememberControl(binding.format, input);
    });
  }

  unbind(input: HTMLElement) {
    const binding = this.bindings.get(input);
    if (binding == null) return;
    input.removeEventListener(binding.eventName, binding.listener);
    if (binding.mouseListener != null) {
      input.removeEventListener('mousedown', binding.mouseListener);
    }
    this.bindings.delete(input);
    this.members.forEach((toolbar) => toolbar.forgetControl(input));
  }

  unbindAll() {
    Array.from(this.bindings.keys()).forEach((input) => this.unbind(input));
  }

  destroy() {
    if (this.destroyed) return;
    this.destroyed = true;
    this.observer?.disconnect();
    this.observer = null;
    this.clearActiveState();
    this.unbindAll();
    ToolbarGroup.groups.delete(this.container);
    domHookDepth += 1;
    try {
      this.container.querySelectorAll('select').forEach((node) => {
        if (node instanceof HTMLSelectElement) {
          forgetPicker(node);
          node.style.display = '';
        }
      });
      this.container.querySelectorAll('.ql-picker').forEach((node) => {
        node.remove();
      });
      this.removeImageInputs();
    } finally {
      domHookDepth -= 1;
    }
  }
}

class Toolbar extends Module<ToolbarProps> {
  static DEFAULTS: ToolbarProps;

  container: HTMLElement | null = null;
  controls: [string, HTMLElement][] = [];
  handlers: Record<string, Handler> = {};
  group: ToolbarGroup | null = null;
  released = false;
  wasConnected = false;

  private readonly onRootFocus = () => {
    this.activate();
  };

  private readonly onEditorChange = (
    event: string,
    range: Range | null,
    _oldRange: unknown,
    source?: string,
  ) => {
    if (this.released) return;
    if (
      event === Quill.events.SELECTION_CHANGE &&
      range != null &&
      source !== Quill.sources.SILENT
    ) {
      this.group?.markActive(this);
    }
    this.refreshUi();
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
      this.container = this.options.container ?? null;
    }
    if (!(this.container instanceof HTMLElement)) {
      debug.error('Container required for toolbar', this.options);
      return;
    }
    this.container.classList.add('ql-toolbar');
    if (this.options.handlers) {
      Object.keys(this.options.handlers).forEach((format) => {
        const handler = this.options.handlers?.[format];
        if (handler) {
          this.addHandler(format, handler);
        }
      });
    }
    this.wasConnected = this.quill.container?.isConnected === true;
    this.group = ToolbarGroup.forContainer(this.container);
    this.group.add(this);
    Array.from(this.container.querySelectorAll('button, select')).forEach(
      (input) => {
        if (input instanceof HTMLElement) this.attach(input);
      },
    );
    this.quill.on(
      Quill.events.EDITOR_CHANGE,
      this.onEditorChange as unknown as Parameters<Quill['on']>[1],
    );
    this.quill.root.addEventListener('focus', this.onRootFocus);
    if (this.quill.container != null) {
      editorToolbarsByContainer.set(this.quill.container, this);
    }
    if (this.quill.root != null) {
      editorToolbarsByRoot.set(this.quill.root, this);
    }
    liveToolbars.add(this);
    installDomHooks();
    ensureLiveObserver();
  }

  addHandler(format: string, handler: Handler) {
    this.handlers[format] = handler;
  }

  attach(input: HTMLElement) {
    if (input.tagName === 'BUTTON') {
      input.setAttribute('type', 'button');
    }
    this.group?.bind(input, true);
  }

  rememberControl(format: string, input: HTMLElement) {
    if (this.controls.some((pair) => pair[1] === input)) return;
    this.controls.push([format, input]);
  }

  forgetControl(input: HTMLElement) {
    this.controls = this.controls.filter((pair) => pair[1] !== input);
  }

  fileAccept() {
    const uploader = this.quill.uploader as unknown as {
      options?: { mimetypes?: string[] };
    };
    return (uploader?.options?.mimetypes ?? []).join(', ');
  }

  update(range: Range | null) {
    if (this.released || !this.shouldRefreshUi()) return;
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

  shouldRefreshUi() {
    if (this.released) return false;
    if (this.group == null) return true;
    return this.group.current() === this;
  }

  refreshUi() {
    if (this.released || !this.shouldRefreshUi()) return;
    const [range] = this.quill.selection.getRange();
    if (
      range == null &&
      this.container != null &&
      document.activeElement != null &&
      this.container.contains(document.activeElement)
    ) {
      return;
    }
    this.update(range);
    this.refreshPickers();
  }

  refreshPickers() {
    this.container?.querySelectorAll('select').forEach((node) => {
      if (node instanceof HTMLSelectElement) getPicker(node)?.update();
    });
  }

  activate() {
    if (this.released || this.group == null) return;
    this.group.markActive(this);
    this.refreshUi();
  }

  onEnabledChange() {
    if (this.released || !this.shouldRefreshUi()) return;
    this.group?.syncDisabled();
  }

  needsRelease() {
    const connected = this.quill.container?.isConnected === true;
    if (connected) {
      this.wasConnected = true;
      return false;
    }
    return this.wasConnected;
  }

  release() {
    if (this.released) return;
    this.released = true;
    this.quill.off(
      Quill.events.EDITOR_CHANGE,
      this.onEditorChange as unknown as Parameters<Quill['off']>[1],
    );
    this.quill.root?.removeEventListener('focus', this.onRootFocus);
    liveToolbars.delete(this);
    if (this.quill.container != null) {
      const current = editorToolbarsByContainer.get(this.quill.container);
      if (current === this)
        editorToolbarsByContainer.delete(this.quill.container);
    }
    if (this.quill.root != null) {
      const current = editorToolbarsByRoot.get(this.quill.root);
      if (current === this) editorToolbarsByRoot.delete(this.quill.root);
    }
    domHookDepth += 1;
    try {
      this.group?.remove(this);
    } finally {
      domHookDepth -= 1;
    }
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
