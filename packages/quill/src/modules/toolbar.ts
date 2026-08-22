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

interface AttachedControl {
  format: string;
  eventName: 'click' | 'change';
  listener: EventListener;
}

const sharedToolbars = new WeakMap<HTMLElement, SharedToolbar>();

class SharedToolbar {
  static acquire(container: HTMLElement) {
    let shared = sharedToolbars.get(container);
    if (shared == null) {
      shared = new SharedToolbar(container);
      sharedToolbars.set(container, shared);
    }
    return shared;
  }

  static get(container: HTMLElement) {
    return sharedToolbars.get(container);
  }

  container: HTMLElement;
  controls: [string, HTMLElement][] = [];
  toolbars = new Set<Toolbar>();
  active: Toolbar | null = null;
  private hadMultiple = false;

  private attached = new WeakMap<HTMLElement, AttachedControl>();
  private observer: MutationObserver;
  private observing = false;
  private mousedownHandler: ((e: MouseEvent) => void) | null = null;

  constructor(container: HTMLElement) {
    this.container = container;
    this.observer = new MutationObserver((mutations) => {
      this.handleMutations(mutations);
      this.pruneDeadEditors();
    });
  }

  register(toolbar: Toolbar) {
    this.toolbars.add(toolbar);
    if (this.toolbars.size > 1) {
      this.hadMultiple = true;
    }
    this.ensureListening();
  }

  unregister(toolbar: Toolbar) {
    toolbar.unbindFromEditor();
    const wasActive = this.active === toolbar;
    this.toolbars.delete(toolbar);
    if (wasActive) {
      this.active = null;
    }
    if (this.toolbars.size === 0) {
      this.cleanup();
      return;
    }
    if (wasActive) {
      this.syncDisabled();
      this.clearActiveStyles();
      this.syncThemeUI();
      this.updatePickers();
    }
  }

  getTarget() {
    this.pruneDeadEditors();
    return this.currentTarget();
  }

  setActive(toolbar: Toolbar) {
    if (!this.toolbars.has(toolbar) || !toolbar.isLive()) {
      return;
    }
    this.active = toolbar;
    this.syncDisabled();
    const [range] = toolbar.quill.selection.getRange();
    toolbar.update(range);
    this.updatePickers();
    this.syncThemeUI();
  }

  onEnableChange(toolbar: Toolbar) {
    if (this.currentTarget() !== toolbar) return;
    this.syncDisabled();
    const [range] = toolbar.quill.selection.getRange();
    toolbar.update(range);
    this.updatePickers();
    this.syncThemeUI();
  }

  attach(input: HTMLElement) {
    if (this.attached.has(input)) return;
    let format = Array.from(input.classList).find((className) => {
      return className.indexOf('ql-') === 0;
    });
    if (!format) return;
    format = format.slice('ql-'.length);
    if (input.tagName === 'BUTTON') {
      input.setAttribute('type', 'button');
    }
    if (!this.canAttach(format)) {
      debug.warn('ignoring attaching to nonexistent format', format, input);
      return;
    }
    const eventName = input.tagName === 'SELECT' ? 'change' : 'click';
    const listener = (e: Event) => {
      this.handleAction(input, format, e);
    };
    input.addEventListener(eventName, listener);
    this.attached.set(input, { format, eventName, listener });
    this.controls.push([format, input]);
  }

  detach(input: HTMLElement) {
    const attached = this.attached.get(input);
    if (attached == null) return;
    input.removeEventListener(attached.eventName, attached.listener);
    this.attached.delete(input);
    const index = this.controls.findIndex(([, el]) => el === input);
    if (index !== -1) {
      this.controls.splice(index, 1);
    }
  }

  private canAttach(format: string) {
    return Array.from(this.toolbars).some((toolbar) => {
      return (
        toolbar.handlers[format] != null ||
        toolbar.quill.scroll.query(format) != null
      );
    });
  }

  private handleAction(input: HTMLElement, format: string, e: Event) {
    const toolbar = this.getTarget();
    if (toolbar == null || !toolbar.quill.isEnabled()) {
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
      e.preventDefault();
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
  }

  private handleMutations(mutations: MutationRecord[]) {
    mutations.forEach((mutation) => {
      Array.from(mutation.removedNodes).forEach((node) => {
        this.forEachControl(node, (input) => this.detach(input));
      });
      Array.from(mutation.addedNodes).forEach((node) => {
        if (!(node instanceof Element)) return;
        if (node !== this.container && !this.container.contains(node)) {
          return;
        }
        this.forEachControl(node, (input) => this.attach(input));
      });
    });
  }

  private forEachControl(node: Node, callback: (input: HTMLElement) => void) {
    if (
      node instanceof HTMLElement &&
      (node.tagName === 'BUTTON' || node.tagName === 'SELECT')
    ) {
      callback(node);
    }
    if (node instanceof Element) {
      Array.from(node.querySelectorAll('button, select')).forEach((input) => {
        callback(input as HTMLElement);
      });
    }
  }

  private pruneDeadEditors() {
    Array.from(this.toolbars).forEach((toolbar) => {
      if (!toolbar.isLive()) {
        this.unregister(toolbar);
      }
    });
  }

  private ensureListening() {
    if (!this.observing) {
      this.observer.observe(document.documentElement, {
        childList: true,
        subtree: true,
      });
      this.observing = true;
    }
    if (this.mousedownHandler == null) {
      this.mousedownHandler = (e: MouseEvent) => {
        const target = e.target;
        if (!(target instanceof Element)) return;
        if (target.closest('input, textarea, select')) return;
        e.preventDefault();
      };
      this.container.addEventListener('mousedown', this.mousedownHandler);
    }
  }

  private currentTarget() {
    if (this.active && this.toolbars.has(this.active) && this.active.isLive()) {
      return this.active;
    }
    if (this.toolbars.size === 1 && !this.hadMultiple) {
      const only = Array.from(this.toolbars)[0];
      return only.isLive() ? only : null;
    }
    return null;
  }

  private syncDisabled() {
    const target = this.currentTarget();
    const enabled = target != null && target.quill.isEnabled();
    Array.from(this.container.querySelectorAll('button, select')).forEach(
      (input) => {
        (input as HTMLButtonElement | HTMLSelectElement).disabled = !enabled;
      },
    );
  }

  private clearActiveStyles() {
    this.controls.forEach(([, input]) => {
      if (input.tagName === 'SELECT') {
        (input as HTMLSelectElement).selectedIndex = -1;
        (input as HTMLSelectElement).value = '';
      } else {
        input.classList.remove('ql-active');
        input.setAttribute('aria-pressed', 'false');
      }
    });
  }

  private updatePickers() {
    this.toolbars.forEach((toolbar) => {
      const pickers = (
        toolbar.quill.theme as { pickers?: Array<{ update: () => void }> }
      ).pickers;
      pickers?.forEach((picker) => {
        picker.update();
      });
    });
  }

  syncThemeUI() {
    const fileInput = this.container.querySelector<HTMLInputElement>(
      'input.ql-image[type=file]',
    );
    if (fileInput == null) return;
    const target = this.currentTarget();
    if (target == null || !target.quill.isEnabled()) {
      fileInput.disabled = true;
      return;
    }
    fileInput.disabled = false;
    const mimetypes = target.quill.uploader?.options?.mimetypes;
    if (mimetypes) {
      fileInput.setAttribute('accept', mimetypes.join(', '));
    }
  }

  private removeFileInput() {
    Array.from(
      this.container.querySelectorAll('input.ql-image[type=file]'),
    ).forEach((input) => {
      input.remove();
    });
  }

  private cleanup() {
    Array.from(this.controls).forEach(([, input]) => {
      this.detach(input);
    });
    this.removeFileInput();
    this.observer.disconnect();
    this.observing = false;
    if (this.mousedownHandler != null) {
      this.container.removeEventListener('mousedown', this.mousedownHandler);
      this.mousedownHandler = null;
    }
    this.active = null;
    this.hadMultiple = false;
  }
}

export function getActiveToolbar(
  container: HTMLElement | null | undefined,
): Toolbar | null {
  if (!(container instanceof HTMLElement)) return null;
  return SharedToolbar.get(container)?.getTarget() ?? null;
}

class Toolbar extends Module<ToolbarProps> {
  static DEFAULTS: ToolbarProps;

  container?: HTMLElement | null;
  controls: [string, HTMLElement][];
  handlers: Record<string, Handler>;
  private shared?: SharedToolbar;
  private originalEnable?: Quill['enable'];
  private onEditorChange?: () => void;
  private onSelectionChange?: (range: Range | null) => void;
  private onFocus?: () => void;

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
    this.shared = SharedToolbar.acquire(this.container);
    this.controls = this.shared.controls;
    this.handlers = {};
    if (this.options.handlers) {
      Object.keys(this.options.handlers).forEach((format) => {
        const handler = this.options.handlers?.[format];
        if (handler) {
          this.addHandler(format, handler);
        }
      });
    }
    this.shared.register(this);
    this.bindToEditor();
    Array.from(this.container.querySelectorAll('button, select')).forEach(
      (input) => {
        this.attach(input as HTMLElement);
      },
    );
    this.shared.syncThemeUI();
    if (this.shared.getTarget() === this) {
      this.shared.onEnableChange(this);
    }
  }

  addHandler(format: string, handler: Handler) {
    this.handlers[format] = handler;
  }

  attach(input: HTMLElement) {
    this.shared?.attach(input);
  }

  destroy() {
    this.shared?.unregister(this);
  }

  isLive() {
    return this.quill.root.isConnected;
  }

  bindToEditor() {
    this.onEditorChange = () => {
      if (this.shared?.getTarget() !== this) return;
      const [range] = this.quill.selection.getRange(); // quill.getSelection triggers update
      this.update(range);
    };
    this.quill.on(Quill.events.EDITOR_CHANGE, this.onEditorChange);

    this.onSelectionChange = (range) => {
      if (range != null && this.isLive()) {
        this.shared?.setActive(this);
      }
    };
    this.quill.on(Quill.events.SELECTION_CHANGE, this.onSelectionChange);

    this.onFocus = () => {
      if (this.isLive()) {
        this.shared?.setActive(this);
      }
    };
    this.quill.container.addEventListener('focusin', this.onFocus);

    this.originalEnable = this.quill.enable.bind(this.quill);
    this.quill.enable = (enabled = true) => {
      this.originalEnable?.(enabled);
      this.shared?.onEnableChange(this);
    };
  }

  unbindFromEditor() {
    if (this.onEditorChange) {
      this.quill.off(Quill.events.EDITOR_CHANGE, this.onEditorChange);
      this.onEditorChange = undefined;
    }
    if (this.onSelectionChange) {
      this.quill.off(Quill.events.SELECTION_CHANGE, this.onSelectionChange);
      this.onSelectionChange = undefined;
    }
    if (this.onFocus) {
      this.quill.container.removeEventListener('focusin', this.onFocus);
      this.onFocus = undefined;
    }
    if (this.originalEnable) {
      this.quill.enable = this.originalEnable;
      this.originalEnable = undefined;
    }
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
