import Delta from 'quill-delta';
import { EmbedBlot, Scope } from 'parchment';
import Quill from '../core/quill.js';
import logger from '../core/logger.js';
import Module from '../core/module.js';
import type { Range } from '../core/selection.js';
import type Picker from '../ui/picker.js';

const debug = logger('quill:toolbar');

type Handler = (this: Toolbar, value: any) => void;
type ActiveChangeHandler = (toolbar: Toolbar | null) => void;
type ControlChangeHandler = (input: HTMLElement) => void;

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

const toolbarGroups = new WeakMap<HTMLElement, ToolbarGroup>();

class ToolbarGroup {
  toolbars: Toolbar[] = [];
  active: Toolbar | null = null;
  controls = new Map<HTMLElement, string>();
  pickers = new Set<Picker>();
  activeChangeHandlers = new Map<Toolbar, Set<ActiveChangeHandler>>();
  controlChangeHandlers = new Map<Toolbar, Set<ControlChangeHandler>>();
  private observer: MutationObserver;
  private documentObserver: MutationObserver;

  constructor(public container: HTMLElement) {
    this.container.addEventListener('click', this.handleClick);
    this.container.addEventListener('change', this.handleChange);
    this.observer = new MutationObserver(() => {
      this.syncControls();
      this.update();
    });
    this.observer.observe(this.container, { childList: true, subtree: true });
    this.documentObserver = new MutationObserver(() => this.prune());
    this.documentObserver.observe(document, { childList: true, subtree: true });
  }

  register(toolbar: Toolbar) {
    this.toolbars.push(toolbar);
    this.syncControls();
    this.update();
  }

  unregister(toolbar: Toolbar) {
    if (this.active === toolbar) {
      this.setActive(null);
    }
    const index = this.toolbars.indexOf(toolbar);
    if (index !== -1) {
      this.toolbars.splice(index, 1);
    }
    this.activeChangeHandlers.delete(toolbar);
    this.controlChangeHandlers.delete(toolbar);
    if (this.toolbars.length === 0) {
      this.dispose();
    } else {
      this.syncControls();
      this.update();
    }
  }

  dispose() {
    this.container.removeEventListener('click', this.handleClick);
    this.container.removeEventListener('change', this.handleChange);
    this.observer.disconnect();
    this.documentObserver.disconnect();
    this.pickers.forEach((picker) => picker.destroy());
    this.pickers.clear();
    this.container
      .querySelectorAll('input[data-ql-image-input]')
      .forEach((input) => input.remove());
    this.controls.clear();
    toolbarGroups.delete(this.container);
  }

  getActive() {
    this.prune();
    return this.active;
  }

  activate(toolbar: Toolbar) {
    if (this.toolbars.includes(toolbar) && this.isLive(toolbar)) {
      this.setActive(toolbar);
    }
  }

  addControl(input: HTMLElement, format: string) {
    if (!this.controls.has(input)) {
      if (input.tagName === 'BUTTON') {
        input.setAttribute('type', 'button');
      }
      this.controls.set(input, format);
      this.controlChangeHandlers.forEach((handlers) => {
        handlers.forEach((handler) => handler(input));
      });
    }
  }

  addPicker(picker: Picker) {
    this.pickers.add(picker);
    this.update();
  }

  onActiveChange(toolbar: Toolbar, handler: ActiveChangeHandler) {
    let handlers = this.activeChangeHandlers.get(toolbar);
    if (handlers == null) {
      handlers = new Set();
      this.activeChangeHandlers.set(toolbar, handlers);
    }
    handlers.add(handler);
    handler(this.active);
  }

  onControlChange(toolbar: Toolbar, handler: ControlChangeHandler) {
    let handlers = this.controlChangeHandlers.get(toolbar);
    if (handlers == null) {
      handlers = new Set();
      this.controlChangeHandlers.set(toolbar, handlers);
    }
    handlers.add(handler);
  }

  handleEditorChange(
    toolbar: Toolbar,
    type: string,
    range: Range | null,
    source: string,
  ) {
    if (!this.isLive(toolbar)) {
      this.prune();
      return;
    }
    if (
      toolbar.quill.hasFocus() ||
      (type === Quill.events.SELECTION_CHANGE &&
        range != null &&
        source === Quill.sources.USER)
    ) {
      this.activate(toolbar);
    }
    if (this.active === toolbar) {
      this.update();
    }
  }

  handleEnabledChange(toolbar: Toolbar) {
    if (this.active === toolbar) {
      this.update();
    }
  }

  private handleClick = (event: Event) => {
    const input = this.getControl(event);
    if (input?.tagName === 'BUTTON') {
      this.handleControl(input, event);
    }
  };

  private handleChange = (event: Event) => {
    const input = this.getControl(event);
    if (input?.tagName === 'SELECT') {
      this.handleControl(input, event);
    }
  };

  private getControl(event: Event) {
    if (!(event.target instanceof Element)) return null;
    const input = event.target.closest('button, select');
    return input instanceof HTMLElement && this.container.contains(input)
      ? input
      : null;
  }

  private handleControl(input: HTMLElement, event: Event) {
    this.syncControls();
    const toolbar = this.getActive();
    if (
      toolbar == null ||
      !toolbar.quill.isEnabled() ||
      !toolbar.supports(this.controls.get(input) || getFormat(input))
    ) {
      return;
    }
    toolbar.handleControl(input, event);
  }

  private setActive(toolbar: Toolbar | null) {
    if (this.active === toolbar) {
      this.update();
      return;
    }
    this.active = toolbar;
    this.update();
    this.activeChangeHandlers.forEach((handlers) => {
      handlers.forEach((handler) => handler(toolbar));
    });
  }

  private isLive(toolbar: Toolbar) {
    return toolbar.quill.root.isConnected;
  }

  private prune() {
    this.toolbars
      .slice()
      .filter((toolbar) => !this.isLive(toolbar))
      .forEach((toolbar) => toolbar.destroy());
    if (this.active != null && !this.isLive(this.active)) {
      this.setActive(null);
    }
  }

  private syncControls() {
    const current = new Set<HTMLElement>();
    Array.from(
      this.container.querySelectorAll<HTMLElement>('button, select'),
    ).forEach((input) => {
      current.add(input);
      const format = getFormat(input);
      if (format != null) {
        this.addControl(input, format);
      }
    });
    this.controls.forEach((_format, input) => {
      if (!current.has(input)) {
        this.controls.delete(input);
      }
    });
    this.pickers.forEach((picker) => {
      if (!this.container.contains(picker.select)) {
        picker.destroy();
        this.pickers.delete(picker);
      }
    });
  }

  update() {
    const toolbar = this.active;
    const range =
      toolbar == null ? null : toolbar.quill.selection.getRange()[0];
    const formats =
      toolbar == null || range == null ? {} : toolbar.quill.getFormat(range);

    this.syncControls();
    this.controls.forEach((format, input) => {
      updateControl(input, format, formats, range);
      setControlDisabled(
        input,
        toolbar == null ||
          !toolbar.quill.isEnabled() ||
          !toolbar.supports(format),
      );
    });
    this.pickers.forEach((picker) => {
      const format = getFormat(picker.select);
      picker.setDisabled(
        toolbar == null ||
          !toolbar.quill.isEnabled() ||
          !toolbar.supports(format),
      );
      picker.update();
    });
    this.container
      .querySelectorAll<HTMLInputElement>('input[data-ql-image-input]')
      .forEach((input) => {
        if (toolbar == null) {
          input.remove();
        } else {
          input.disabled = !toolbar.quill.isEnabled();
          input.accept = toolbar.quill.uploader.getMimetypes().join(', ');
        }
      });
  }
}

export function getToolbarGroup(container: HTMLElement) {
  let group = toolbarGroups.get(container);
  if (group == null) {
    group = new ToolbarGroup(container);
    toolbarGroups.set(container, group);
  }
  return group;
}

class Toolbar extends Module<ToolbarProps> {
  static DEFAULTS: ToolbarProps;

  container?: HTMLElement | null;
  controls: [string, HTMLElement][];
  handlers: Record<string, Handler>;
  private group?: ToolbarGroup;
  private handleEditorChange = (
    type: string,
    range: Range | null,
    _oldRange: Range | null,
    source: string,
  ) => {
    this.group?.handleEditorChange(this, type, range, source);
  };
  private handleEnabledChange = () => {
    this.group?.handleEnabledChange(this);
  };
  private handleFocus = () => {
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
      return;
    }
    this.container.classList.add('ql-toolbar');
    this.controls = [];
    this.handlers = {};
    this.group = getToolbarGroup(this.container);
    this.group.register(this);
    if (this.options.handlers) {
      Object.keys(this.options.handlers).forEach((format) => {
        const handler = this.options.handlers?.[format];
        if (handler) {
          this.addHandler(format, handler);
        }
      });
    }
    Array.from(this.container.querySelectorAll('button, select')).forEach(
      (input) => {
        // @ts-expect-error
        this.attach(input);
      },
    );
    this.quill.emitter.on(Quill.events.EDITOR_CHANGE, this.handleEditorChange);
    this.quill.emitter.on(Quill.events.ENABLE_CHANGE, this.handleEnabledChange);
    this.quill.root.addEventListener('focusin', this.handleFocus);
    this.quill.root.addEventListener('mousedown', this.handleFocus);
    this.group.update();
  }

  addHandler(format: string, handler: Handler) {
    this.handlers[format] = handler;
  }

  attach(input: HTMLElement) {
    const format = getFormat(input);
    if (!format) return;
    if (input.tagName === 'BUTTON') {
      input.setAttribute('type', 'button');
    }
    if (!this.supports(format)) {
      debug.warn('ignoring attaching to nonexistent format', format, input);
      return;
    }
    if (!this.controls.some(([, control]) => control === input)) {
      this.controls.push([format, input]);
    }
    this.group?.addControl(input, format);
  }

  addPicker(picker: Picker) {
    this.group?.addPicker(picker);
  }

  onActiveChange(handler: ActiveChangeHandler) {
    this.group?.onActiveChange(this, handler);
  }

  onControlChange(handler: ControlChangeHandler) {
    this.group?.onControlChange(this, handler);
  }

  getActive() {
    return this.group?.getActive() || null;
  }

  supports(format: string | null) {
    return (
      format != null &&
      (this.handlers[format] != null || this.quill.scroll.query(format) != null)
    );
  }

  handleControl(input: HTMLElement, event: Event) {
    const format = getFormat(input);
    if (format == null) return;
    let value;
    if (input.tagName === 'SELECT') {
      const select = input as HTMLSelectElement;
      if (select.selectedIndex < 0) return;
      const selected = select.options[select.selectedIndex];
      value = selected.hasAttribute('selected')
        ? false
        : selected.value || false;
    } else {
      const button = input as HTMLButtonElement;
      value = button.classList.contains('ql-active')
        ? false
        : button.value || !button.hasAttribute('value');
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
      if (range == null) return;
      value = prompt(`Enter ${format}`); // eslint-disable-line no-alert
      if (!value) return;
      this.quill.updateContents(
        new Delta()
          .retain(range.index)
          .delete(range.length)
          .insert({ [format]: value }),
        Quill.sources.USER,
      );
    } else {
      this.quill.format(format, value, Quill.sources.USER);
    }
    this.update(range);
  }

  destroy() {
    this.quill.emitter.off(Quill.events.EDITOR_CHANGE, this.handleEditorChange);
    this.quill.emitter.off(
      Quill.events.ENABLE_CHANGE,
      this.handleEnabledChange,
    );
    this.quill.root.removeEventListener('focusin', this.handleFocus);
    this.quill.root.removeEventListener('mousedown', this.handleFocus);
    if (this.group) {
      const group = this.group;
      this.group = undefined;
      group.unregister(this);
    }
  }

  update(range: Range | null) {
    if (this.group?.active === this) {
      this.group.update();
    } else if (this.group == null) {
      const formats = range == null ? {} : this.quill.getFormat(range);
      this.controls.forEach(([format, input]) =>
        updateControl(input, format, formats, range),
      );
    }
  }
}

function getFormat(input: HTMLElement) {
  const className = Array.from(input.classList).find((name) =>
    name.startsWith('ql-'),
  );
  return className == null ? null : className.slice('ql-'.length);
}

function setControlDisabled(input: HTMLElement, disabled: boolean) {
  if ('disabled' in input) {
    (input as HTMLButtonElement | HTMLSelectElement).disabled = disabled;
  }
}

function updateControl(
  input: HTMLElement,
  format: string,
  formats: Record<string, unknown>,
  range: Range | null,
) {
  if (input.tagName === 'SELECT') {
    const select = input as HTMLSelectElement;
    let option: HTMLOptionElement | undefined;
    if (range != null && formats[format] == null) {
      option = Array.from(select.options).find((item) =>
        item.hasAttribute('selected'),
      );
    } else if (range != null && !Array.isArray(formats[format])) {
      const value = formats[format];
      option = Array.from(select.options).find(
        (item) => item.value === String(value),
      );
    }
    if (option == null) {
      select.value = '';
      select.selectedIndex = -1;
    } else {
      select.selectedIndex = Array.from(select.options).indexOf(option);
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
