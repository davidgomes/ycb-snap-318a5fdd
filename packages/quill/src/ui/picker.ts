import DropdownIcon from '../assets/icons/dropdown.svg';

let optionsCounter = 0;

const pickersBySelect = new WeakMap<HTMLSelectElement, Picker>();

export function getPicker(select: HTMLSelectElement): Picker | undefined {
  const picker = pickersBySelect.get(select);
  if (picker == null) return undefined;
  // A detached toolbar still owns its picker. Only drop the mapping when the
  // select is on the page and the wrapper has been removed.
  if (select.isConnected && !picker.container.isConnected) {
    pickersBySelect.delete(select);
    return undefined;
  }
  return picker;
}

export function forgetPicker(select: HTMLSelectElement) {
  pickersBySelect.delete(select);
}

function toggleAriaAttribute(element: HTMLElement, attribute: string) {
  element.setAttribute(
    attribute,
    `${!(element.getAttribute(attribute) === 'true')}`,
  );
}

class Picker {
  select: HTMLSelectElement;
  container: HTMLElement;
  label: HTMLElement;

  constructor(select: HTMLSelectElement) {
    this.select = select;
    this.container = document.createElement('span');
    this.buildPicker();
    this.select.style.display = 'none';
    // @ts-expect-error Fix me later
    this.select.parentNode.insertBefore(this.container, this.select);

    this.label.addEventListener('mousedown', (event) => {
      // Keep the editor selection in place while the menu is used.
      event.preventDefault();
      if (this.isDisabled()) return;
      this.togglePicker();
    });
    this.label.addEventListener('keydown', (event) => {
      if (this.isDisabled()) {
        event.preventDefault();
        return;
      }
      switch (event.key) {
        case 'Enter':
          this.togglePicker();
          break;
        case 'Escape':
          this.escape();
          event.preventDefault();
          break;
        default:
      }
    });
    this.select.addEventListener('change', this.update.bind(this));
    pickersBySelect.set(this.select, this);
    this.reflectDisabled();
  }

  isDisabled() {
    return this.select.disabled;
  }

  reflectDisabled() {
    const disabled = this.isDisabled();
    this.container.classList.toggle('ql-disabled', disabled);
    this.container.toggleAttribute('disabled', disabled);
    this.label.classList.toggle('ql-disabled', disabled);
    this.label.toggleAttribute('disabled', disabled);
    this.label.setAttribute('aria-disabled', disabled ? 'true' : 'false');
    if (disabled && this.container.classList.contains('ql-expanded')) {
      this.close();
    }
  }

  togglePicker() {
    if (this.isDisabled()) return;
    this.container.classList.toggle('ql-expanded');
    // Toggle aria-expanded and aria-hidden to make the picker accessible
    toggleAriaAttribute(this.label, 'aria-expanded');
    // @ts-expect-error
    toggleAriaAttribute(this.options, 'aria-hidden');
  }

  buildItem(option: HTMLOptionElement) {
    const item = document.createElement('span');
    // @ts-expect-error
    item.tabIndex = '0';
    item.setAttribute('role', 'button');
    item.classList.add('ql-picker-item');
    const value = option.getAttribute('value');
    if (value) {
      item.setAttribute('data-value', value);
    }
    if (option.textContent) {
      item.setAttribute('data-label', option.textContent);
    }
    item.addEventListener('mousedown', (event) => {
      event.preventDefault();
    });
    item.addEventListener('click', () => {
      this.selectItem(item, true);
    });
    item.addEventListener('keydown', (event) => {
      switch (event.key) {
        case 'Enter':
          this.selectItem(item, true);
          event.preventDefault();
          break;
        case 'Escape':
          this.escape();
          event.preventDefault();
          break;
        default:
      }
    });

    return item;
  }

  buildLabel() {
    const label = document.createElement('span');
    label.classList.add('ql-picker-label');
    label.innerHTML = DropdownIcon;
    // @ts-expect-error
    label.tabIndex = '0';
    label.setAttribute('role', 'button');
    label.setAttribute('aria-expanded', 'false');
    this.container.appendChild(label);
    return label;
  }

  buildOptions() {
    const options = document.createElement('span');
    options.classList.add('ql-picker-options');

    // Don't want screen readers to read this until options are visible
    options.setAttribute('aria-hidden', 'true');
    // @ts-expect-error
    options.tabIndex = '-1';

    // Need a unique id for aria-controls
    options.id = `ql-picker-options-${optionsCounter}`;
    optionsCounter += 1;
    this.label.setAttribute('aria-controls', options.id);

    // @ts-expect-error
    this.options = options;

    Array.from(this.select.options).forEach((option) => {
      const item = this.buildItem(option);
      options.appendChild(item);
      if (option.selected === true) {
        this.selectItem(item);
      }
    });
    this.container.appendChild(options);
  }

  buildPicker() {
    Array.from(this.select.attributes).forEach((item) => {
      this.container.setAttribute(item.name, item.value);
    });
    this.container.classList.add('ql-picker');
    this.label = this.buildLabel();
    this.buildOptions();
  }

  escape() {
    // Close menu and return focus to trigger label
    this.close();
    // Need setTimeout for accessibility to ensure that the browser executes
    // focus on the next process thread and after any DOM content changes
    setTimeout(() => this.label.focus(), 1);
  }

  close() {
    this.container.classList.remove('ql-expanded');
    this.label.setAttribute('aria-expanded', 'false');
    // @ts-expect-error
    this.options.setAttribute('aria-hidden', 'true');
  }

  selectItem(item: HTMLElement | null, trigger = false) {
    if (trigger && this.isDisabled()) return;
    const selected = this.container.querySelector('.ql-selected');
    if (item === selected) return;
    if (selected != null) {
      selected.classList.remove('ql-selected');
    }
    if (item == null) return;
    item.classList.add('ql-selected');
    // @ts-expect-error Fix me later
    this.select.selectedIndex = Array.from(item.parentNode.children).indexOf(
      item,
    );
    if (item.hasAttribute('data-value')) {
      // @ts-expect-error Fix me later
      this.label.setAttribute('data-value', item.getAttribute('data-value'));
    } else {
      this.label.removeAttribute('data-value');
    }
    if (item.hasAttribute('data-label')) {
      // @ts-expect-error Fix me later
      this.label.setAttribute('data-label', item.getAttribute('data-label'));
    } else {
      this.label.removeAttribute('data-label');
    }
    if (trigger) {
      this.select.dispatchEvent(new Event('change'));
      this.close();
    }
  }

  update() {
    this.reflectDisabled();
    let option;
    if (this.select.selectedIndex > -1) {
      const item =
        // @ts-expect-error Fix me later
        this.container.querySelector('.ql-picker-options').children[
          this.select.selectedIndex
        ];
      option = this.select.options[this.select.selectedIndex];
      // @ts-expect-error
      this.selectItem(item);
    } else {
      this.selectItem(null);
    }
    const isActive =
      option != null &&
      option !== this.select.querySelector('option[selected]');
    this.label.classList.toggle('ql-active', isActive);
  }
}

export default Picker;
