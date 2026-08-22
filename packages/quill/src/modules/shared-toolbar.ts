import Quill from '../core/quill.js';
import type Picker from '../ui/picker.js';
import type Toolbar from './toolbar.js';

type ControlBinding = {
  eventName: string;
  listener: EventListener;
};

class SharedToolbarContext {
  static contexts = new Map<HTMLElement, SharedToolbarContext>();

  static get(container: HTMLElement): SharedToolbarContext | undefined {
    return SharedToolbarContext.contexts.get(container);
  }

  static getOrCreate(container: HTMLElement): SharedToolbarContext {
    let context = SharedToolbarContext.contexts.get(container);
    if (!context) {
      context = new SharedToolbarContext(container);
      SharedToolbarContext.contexts.set(container, context);
    }
    return context;
  }

  container: HTMLElement;
  toolbars = new Set<Toolbar>();
  active: Toolbar | null = null;
  pickers: Picker[] | null = null;
  controlBindings = new Map<HTMLElement, ControlBinding>();
  private observer: MutationObserver;
  private editorObservers = new Map<Toolbar, MutationObserver>();
  private enableObservers = new Map<Toolbar, MutationObserver>();

  constructor(container: HTMLElement) {
    this.container = container;
    this.observer = new MutationObserver((mutations) => {
      mutations.forEach((mutation) => {
        mutation.addedNodes.forEach((node) => {
          if (node instanceof HTMLElement) {
            this.scanForControls(node);
          }
        });
        mutation.removedNodes.forEach((node) => {
          if (node instanceof HTMLElement) {
            this.unbindRemovedControls(node);
          }
        });
      });
    });
    this.observer.observe(container, { childList: true, subtree: true });
  }

  add(toolbar: Toolbar) {
    this.toolbars.add(toolbar);

    toolbar.quill.on(
      Quill.events.SELECTION_CHANGE,
      (range, _oldRange, source) => {
        if (source === Quill.sources.USER && range != null) {
          this.setActive(toolbar);
        }
      },
    );

    const editorObserver = new MutationObserver(() => {
      if (!toolbar.quill.root.isConnected) {
        this.remove(toolbar);
      }
    });
    editorObserver.observe(document.body, { childList: true, subtree: true });
    this.editorObservers.set(toolbar, editorObserver);

    const enableObserver = new MutationObserver(() => {
      if (this.active === toolbar) {
        this.refreshActiveState();
      }
    });
    enableObserver.observe(toolbar.quill.container, {
      attributes: true,
      attributeFilter: ['class'],
    });
    this.enableObservers.set(toolbar, enableObserver);

    const originalEnable = toolbar.quill.enable.bind(toolbar.quill);
    toolbar.quill.enable = (enabled = true) => {
      originalEnable(enabled);
      if (this.active === toolbar) {
        this.refreshActiveState();
      }
    };

    this.pruneDeadToolbars();

    if (this.active == null) {
      this.setActive(toolbar);
    }
  }

  remove(toolbar: Toolbar) {
    this.toolbars.delete(toolbar);
    this.editorObservers.get(toolbar)?.disconnect();
    this.editorObservers.delete(toolbar);
    this.enableObservers.get(toolbar)?.disconnect();
    this.enableObservers.delete(toolbar);

    if (this.active === toolbar) {
      this.active = null;
      this.clearToolbarState();
      this.destroyIfEmpty();
    } else {
      this.destroyIfEmpty();
    }
  }

  setActive(toolbar: Toolbar) {
    this.pruneDeadToolbars();
    if (!this.toolbars.has(toolbar) || !this.isLive(toolbar)) return;
    this.active = toolbar;
    this.refreshActiveState();
  }

  refreshActiveState() {
    const toolbar = this.active;
    if (toolbar && this.isLive(toolbar)) {
      const [range] = toolbar.quill.selection.getRange();
      toolbar.update(range);
      this.updatePickers();
    } else {
      this.active = null;
      this.clearToolbarState();
    }
  }

  clearToolbarState() {
    const reference = this.getLiveToolbars()[0];
    if (reference) {
      reference.update(null);
    } else {
      this.getControls().forEach((input) => {
        if (input.tagName === 'BUTTON') {
          input.classList.remove('ql-active');
          input.setAttribute('aria-pressed', 'false');
          input.removeAttribute('disabled');
        } else if (input.tagName === 'SELECT') {
          // @ts-expect-error
          input.value = '';
          // @ts-expect-error
          input.selectedIndex = -1;
          input.removeAttribute('disabled');
        }
        input.closest('.ql-picker')?.classList.remove('ql-disabled');
      });
    }
    this.updatePickers();
  }

  bindControl(
    input: HTMLElement,
    format: string,
    handler: (toolbar: Toolbar, event: Event) => void,
  ): boolean {
    if (this.controlBindings.has(input)) {
      return true;
    }
    const eventName = input.tagName === 'SELECT' ? 'change' : 'click';
    const listener = (event: Event) => {
      this.pruneDeadToolbars();
      const active = this.active;
      if (active == null || !this.isLive(active)) return;
      if (this.isDisabled(active)) return;
      handler(active, event);
    };
    input.addEventListener(eventName, listener);
    this.controlBindings.set(input, { eventName, listener });
    return false;
  }

  unbindControl(input: HTMLElement) {
    const binding = this.controlBindings.get(input);
    if (binding) {
      input.removeEventListener(binding.eventName, binding.listener);
      this.controlBindings.delete(input);
    }
    this.toolbars.forEach((toolbar) => {
      toolbar.controls = toolbar.controls.filter(
        ([, control]) => control !== input,
      );
    });
  }

  isControlBound(input: HTMLElement): boolean {
    return this.controlBindings.has(input);
  }

  updatePickers() {
    this.pickers?.forEach((picker) => {
      picker.update();
    });
  }

  setPickers(pickers: Picker[]) {
    if (this.pickers == null) {
      this.pickers = pickers;
    }
  }

  isDisabled(toolbar: Toolbar): boolean {
    return toolbar.quill.container.classList.contains('ql-disabled');
  }

  isLive(toolbar: Toolbar): boolean {
    return toolbar.quill.root.isConnected;
  }

  private getLiveToolbars(): Toolbar[] {
    return [...this.toolbars].filter((toolbar) => this.isLive(toolbar));
  }

  private pruneDeadToolbars() {
    [...this.toolbars].forEach((toolbar) => {
      if (!this.isLive(toolbar)) {
        this.remove(toolbar);
      }
    });
  }

  private destroyIfEmpty() {
    if (this.toolbars.size === 0) {
      this.destroy();
    }
  }

  private destroy() {
    this.observer.disconnect();
    this.editorObservers.forEach((observer) => {
      observer.disconnect();
    });
    this.editorObservers.clear();
    this.enableObservers.forEach((observer) => {
      observer.disconnect();
    });
    this.enableObservers.clear();
    this.controlBindings.forEach(({ eventName, listener }, input) => {
      input.removeEventListener(eventName, listener);
    });
    this.controlBindings.clear();
    this.toolbars.clear();
    this.active = null;
    this.pickers = null;
    SharedToolbarContext.contexts.delete(this.container);
  }

  private getControls(): HTMLElement[] {
    return Array.from(
      this.container.querySelectorAll<HTMLElement>('button, select'),
    );
  }

  private scanForControls(node: HTMLElement) {
    const controls: HTMLElement[] = [];
    if (node.matches('button, select')) {
      controls.push(node);
    }
    controls.push(...node.querySelectorAll<HTMLElement>('button, select'));
    controls.forEach((input) => {
      if (this.controlBindings.has(input)) return;
      this.getLiveToolbars().forEach((toolbar) => {
        toolbar.attach(input);
      });
    });
  }

  private unbindRemovedControls(node: HTMLElement) {
    const controls: HTMLElement[] = [];
    if (node.matches('button, select')) {
      controls.push(node);
    }
    controls.push(...node.querySelectorAll<HTMLElement>('button, select'));
    controls.forEach((input) => {
      this.unbindControl(input);
    });
  }
}

export default SharedToolbarContext;
