import { describe, expect, test, vi } from 'vitest';
import Quill from '../../../src/core/quill.js';
import Toolbar, { addControls } from '../../../src/modules/toolbar.js';
import { normalizeHTML } from '../__helpers__/utils.js';
import SnowTheme from '../../../src/themes/snow.js';
import Clipboard from '../../../src/modules/clipboard.js';
import Keyboard from '../../../src/modules/keyboard.js';
import History from '../../../src/modules/history.js';
import Uploader from '../../../src/modules/uploader.js';
import { createRegistry } from '../__helpers__/factory.js';
import Input from '../../../src/modules/input.js';
import { SizeClass } from '../../../src/formats/size.js';
import Bold from '../../../src/formats/bold.js';
import Link from '../../../src/formats/link.js';
import { AlignClass } from '../../../src/formats/align.js';
import UINode from '../../../src/modules/uiNode.js';

const createContainer = (html = '') => {
  const container = document.body.appendChild(document.createElement('div'));
  container.innerHTML = normalizeHTML(html);
  return container;
};

describe('Toolbar', () => {
  describe('add controls', () => {
    test('single level', () => {
      const container = createContainer();
      addControls(container, ['bold', 'italic']);
      expect(container).toEqualHTML(`
        <span class="ql-formats">
          <button type="button" aria-label="bold" class="ql-bold" aria-pressed="false"></button>
          <button type="button" aria-label="italic" class="ql-italic" aria-pressed="false"></button>
        </span>
      `);
    });

    test('nested group', () => {
      const container = createContainer();
      addControls(container, [
        ['bold', 'italic'],
        ['underline', 'strike'],
      ]);
      expect(container).toEqualHTML(`
        <span class="ql-formats">
          <button type="button" aria-label="bold" class="ql-bold" aria-pressed="false"></button>
          <button type="button" aria-label="italic" class="ql-italic" aria-pressed="false"></button>
        </span>
        <span class="ql-formats">
          <button type="button" aria-label="underline" class="ql-underline" aria-pressed="false"></button>
          <button type="button" aria-label="strike" class="ql-strike" aria-pressed="false"></button>
        </span>
      `);
    });

    test('button value', () => {
      const container = createContainer();
      addControls(container, ['bold', { header: '2' }]);
      expect(container).toEqualHTML(`
        <span class="ql-formats">
          <button type="button" aria-label="bold" class="ql-bold" aria-pressed="false"></button>
          <button type="button" aria-label="header: 2" class="ql-header" aria-pressed="false" value="2"></button>
        </span>
      `);
    });

    test('select', () => {
      const container = createContainer();
      addControls(container, [{ size: ['10px', false, '18px', '32px'] }]);
      expect(container).toEqualHTML(`
        <span class="ql-formats">
          <select class="ql-size">
            <option value="10px"></option>
            <option selected="selected"></option>
            <option value="18px"></option>
            <option value="32px"></option>
          </select>
        </span>
      `);
    });

    test('everything', () => {
      const container = createContainer();
      addControls(container, [
        [
          { font: [false, 'sans-serif', 'monospace'] },
          { size: ['10px', false, '18px', '32px'] },
        ],
        ['bold', 'italic', 'underline', 'strike'],
        [
          { list: 'ordered' },
          { list: 'bullet' },
          { align: [false, 'center', 'right', 'justify'] },
        ],
        ['link', 'image'],
      ]);
      expect(container).toEqualHTML(`
        <span class="ql-formats">
          <select class="ql-font">
            <option selected="selected"></option>
            <option value="sans-serif"></option>
            <option value="monospace"></option>
          </select>
          <select class="ql-size">
            <option value="10px"></option>
            <option selected="selected"></option>
            <option value="18px"></option>
            <option value="32px"></option>
          </select>
        </span>
        <span class="ql-formats">
          <button type="button" aria-label="bold" class="ql-bold" aria-pressed="false"></button>
          <button type="button" aria-label="italic" class="ql-italic" aria-pressed="false"></button>
          <button type="button" aria-label="underline" class="ql-underline" aria-pressed="false"></button>
          <button type="button" aria-label="strike" class="ql-strike" aria-pressed="false"></button>
        </span>
        <span class="ql-formats">
          <button type="button" aria-label="list: ordered" class="ql-list" value="ordered" aria-pressed="false"></button>
          <button type="button" aria-label="list: bullet" class="ql-list" value="bullet" aria-pressed="false"></button>
          <select class="ql-align">
            <option selected="selected"></option>
            <option value="center"></option>
            <option value="right"></option>
            <option value="justify"></option>
          </select>
        </span>
        <span class="ql-formats">
          <button type="button" aria-label="link" class="ql-link" aria-pressed="false"></button>
          <button type="button" aria-label="image" class="ql-image" aria-pressed="false"></button>
        </span>
      `);
    });
  });

  describe('active', () => {
    const setup = () => {
      const container = createContainer(
        `
        <p>0123</p>
        <p><strong>5678</strong></p>
        <p><a href="http://quilljs.com/">0123</a></p>
        <p class="ql-align-center">5678</p>
        <p><span class="ql-size-small">01</span><span class="ql-size-large">23</span></p>
      `,
      );

      Quill.register(
        {
          'themes/snow': SnowTheme,
          'modules/toolbar': Toolbar,
          'modules/clipboard': Clipboard,
          'modules/keyboard': Keyboard,
          'modules/history': History,
          'modules/uploader': Uploader,
          'modules/input': Input,
          'modules/uiNode': UINode,
        },
        true,
      );
      const quill = new Quill(container, {
        modules: {
          toolbar: [
            ['bold', 'link'],
            [{ size: ['small', false, 'large'] }],
            [{ align: '' }, { align: 'center' }],
          ],
        },
        theme: 'snow',
        registry: createRegistry([SizeClass, Bold, AlignClass, Link]),
      });
      return { container, quill };
    };

    test('toggle button', () => {
      const { container, quill } = setup();
      const boldButton = container.parentNode?.querySelector(
        'button.ql-bold',
      ) as HTMLButtonElement;
      quill.setSelection(7);
      expect(boldButton.classList.contains('ql-active')).toBe(true);
      expect(boldButton.getAttribute('aria-pressed')).toBe('true');
      quill.setSelection(2);
      expect(boldButton.classList.contains('ql-active')).toBe(false);
      expect(boldButton.getAttribute('aria-pressed')).toBe('false');
    });

    test('link', () => {
      const { container, quill } = setup();
      const linkButton = container.parentNode?.querySelector(
        'button.ql-link',
      ) as HTMLButtonElement;
      quill.setSelection(12);
      expect(linkButton.classList.contains('ql-active')).toBe(true);
      expect(linkButton.getAttribute('aria-pressed')).toBe('true');
      quill.setSelection(2);
      expect(linkButton.classList.contains('ql-active')).toBe(false);
      expect(linkButton.getAttribute('aria-pressed')).toBe('false');
    });

    test('dropdown', () => {
      const { container, quill } = setup();
      const sizeSelect = container.parentNode?.querySelector(
        'select.ql-size',
      ) as HTMLSelectElement;
      quill.setSelection(21);
      expect(sizeSelect.selectedIndex).toEqual(0);
      quill.setSelection(23);
      expect(sizeSelect.selectedIndex).toEqual(2);
      quill.setSelection(21, 2);
      expect(sizeSelect.selectedIndex).toBeLessThan(0);
      quill.setSelection(2);
      expect(sizeSelect.selectedIndex).toEqual(1);
    });

    test('custom button', () => {
      const { container, quill } = setup();
      const centerButton = container.parentNode?.querySelector(
        'button.ql-align[value="center"]',
      ) as HTMLButtonElement;
      const leftButton = container.parentNode?.querySelector(
        'button.ql-align[value]',
      ) as HTMLButtonElement;
      quill.setSelection(17);
      expect(centerButton.classList.contains('ql-active')).toBe(true);
      expect(leftButton.classList.contains('ql-active')).toBe(false);
      expect(centerButton.getAttribute('aria-pressed')).toBe('true');
      expect(leftButton.getAttribute('aria-pressed')).toBe('false');
      quill.setSelection(2);
      expect(centerButton.classList.contains('ql-active')).toBe(false);
      expect(leftButton.classList.contains('ql-active')).toBe(true);
      expect(centerButton.getAttribute('aria-pressed')).toBe('false');
      expect(leftButton.getAttribute('aria-pressed')).toBe('true');
      quill.blur();
      expect(centerButton.classList.contains('ql-active')).toBe(false);
      expect(leftButton.classList.contains('ql-active')).toBe(false);
      expect(centerButton.getAttribute('aria-pressed')).toBe('false');
      expect(leftButton.getAttribute('aria-pressed')).toBe('false');
    });

    test('update on format', () => {
      const { container, quill } = setup();
      const boldButton = container?.parentNode?.querySelector('button.ql-bold');
      quill.setSelection(1, 2);
      expect(boldButton?.classList.contains('ql-active')).toBe(false);
      quill.format('bold', true, 'user');
      expect(boldButton?.classList.contains('ql-active')).toBe(true);
    });
  });

  describe('shared toolbar container', () => {
    const registerModules = () => {
      Quill.register(
        {
          'themes/snow': SnowTheme,
          'modules/toolbar': Toolbar,
          'modules/clipboard': Clipboard,
          'modules/keyboard': Keyboard,
          'modules/history': History,
          'modules/uploader': Uploader,
          'modules/input': Input,
          'modules/uiNode': UINode,
        },
        true,
      );
    };

    const setupShared = () => {
      registerModules();
      const toolbar = createContainer(`
        <button class="ql-bold" type="button"></button>
        <button class="ql-italic" type="button"></button>
        <button class="ql-image" type="button"></button>
        <select class="ql-size">
          <option selected="selected"></option>
          <option value="small"></option>
          <option value="large"></option>
        </select>
      `);
      const editorA = createContainer('<p><strong>Bold</strong> plain</p>');
      const editorB = createContainer('<p>Second editor</p>');
      const registry = () =>
        createRegistry([SizeClass, Bold, AlignClass, Link]);
      const quillA = new Quill(editorA, {
        modules: { toolbar: { container: toolbar } },
        theme: 'snow',
        registry: registry(),
      });
      const quillB = new Quill(editorB, {
        modules: { toolbar: { container: toolbar } },
        theme: 'snow',
        registry: registry(),
      });
      const bold = toolbar.querySelector('button.ql-bold') as HTMLButtonElement;
      const image = toolbar.querySelector(
        'button.ql-image',
      ) as HTMLButtonElement;
      const size = toolbar.querySelector('select.ql-size') as HTMLSelectElement;
      return { toolbar, quillA, quillB, bold, image, size };
    };

    test('formats the editor that most recently had focus', () => {
      const { quillA, quillB, bold } = setupShared();
      quillA.setSelection(7, 5);
      bold.click();
      expect(quillA.getFormat(7, 1).bold).toBe(true);
      expect(quillB.getFormat(0, 1).bold).toBeFalsy();

      quillB.setSelection(0, 6);
      expect(bold.classList.contains('ql-active')).toBe(false);
      bold.click();
      expect(quillB.getFormat(0, 1).bold).toBe(true);
      expect(quillA.getFormat(7, 1).bold).toBe(true);

      quillA.setSelection(0, 4);
      expect(bold.classList.contains('ql-active')).toBe(true);
      expect(quillB.hasFocus()).toBe(false);
      expect(quillB.getSelection()).toBe(null);
      expect(quillA.hasFocus()).toBe(true);
    });

    test('toolbar click keeps the caret in the active editor', () => {
      const { quillA, quillB, bold } = setupShared();
      quillA.setSelection(0, 4);
      quillB.setSelection(1, 2);
      bold.click();
      expect(quillB.hasFocus()).toBe(true);
      expect(quillA.hasFocus()).toBe(false);
      expect(quillA.getSelection()).toBe(null);
      const selection = quillB.getSelection();
      expect(selection?.index).toBe(1);
      expect(selection?.length).toBe(2);
      expect(quillA.getFormat(0, 1).bold).toBe(true);
      expect(quillB.getFormat(1, 1).bold).toBe(true);
    });

    test('does not duplicate theme-managed toolbar UI', () => {
      const { toolbar, quillA, quillB, image } = setupShared();
      expect(toolbar.querySelectorAll('.ql-picker').length).toBe(1);
      expect(toolbar.querySelectorAll('.ql-picker-options').length).toBe(1);
      expect(toolbar.querySelectorAll('input.ql-image').length).toBe(0);

      const originalClick = HTMLInputElement.prototype.click;
      HTMLInputElement.prototype.click = function click() {
        if (this.type === 'file') return;
        return originalClick.call(this);
      };
      try {
        quillA.focus();
        image.click();
        quillB.focus();
        image.click();
      } finally {
        HTMLInputElement.prototype.click = originalClick;
      }
      expect(toolbar.querySelectorAll('input.ql-image').length).toBe(1);
    });

    test('hidden image input follows the active editor', () => {
      const { toolbar, quillA, quillB, image } = setupShared();
      const originalClick = HTMLInputElement.prototype.click;
      HTMLInputElement.prototype.click = function click() {
        if (this.type === 'file') return;
        return originalClick.call(this);
      };
      const uploadA = vi.spyOn(quillA.uploader, 'upload');
      const uploadB = vi.spyOn(quillB.uploader, 'upload');
      try {
        quillA.setSelection(0, 1);
        image.click();
        const input = toolbar.querySelector(
          'input.ql-image[type=file]',
        ) as HTMLInputElement;
        const mimetypes = (
          quillA.uploader as unknown as { options: { mimetypes: string[] } }
        ).options.mimetypes;
        expect(input.getAttribute('accept')).toBe(mimetypes.join(', '));
        (
          quillB.uploader as unknown as { options: { mimetypes: string[] } }
        ).options.mimetypes = ['image/gif'];
        quillB.setSelection(0, 1);
        expect(input.getAttribute('accept')).toBe('image/gif');
        const file = new File(['x'], 'a.gif', { type: 'image/gif' });
        Object.defineProperty(input, 'files', {
          configurable: true,
          value: [file],
        });
        input.dispatchEvent(new Event('change'));
        expect(uploadB).toHaveBeenCalledTimes(1);
        expect(uploadA).not.toHaveBeenCalled();
      } finally {
        HTMLInputElement.prototype.click = originalClick;
      }
    });

    test('removing the active editor clears shared toolbar state', () => {
      const { toolbar, quillA, quillB, bold, image } = setupShared();
      const originalClick = HTMLInputElement.prototype.click;
      HTMLInputElement.prototype.click = function click() {
        if (this.type === 'file') return;
        return originalClick.call(this);
      };
      try {
        quillB.setSelection(0, 4);
        bold.click();
        expect(bold.classList.contains('ql-active')).toBe(true);
        image.click();
        expect(toolbar.querySelectorAll('input.ql-image').length).toBe(1);
        quillB.container.remove();
        expect(bold.classList.contains('ql-active')).toBe(false);
        expect(toolbar.querySelectorAll('input.ql-image').length).toBe(0);
        const before = quillA.getSemanticHTML();
        bold.click();
        expect(quillA.getSemanticHTML()).toBe(before);
        quillA.setSelection(7, 5);
        bold.click();
        expect(quillA.getFormat(7, 1).bold).toBe(true);
      } finally {
        HTMLInputElement.prototype.click = originalClick;
      }
    });

    test('disables shared controls while the active editor is read-only', () => {
      const { toolbar, quillA, quillB, bold, size } = setupShared();
      quillA.setSelection(7, 5);
      quillA.disable();
      expect(bold.disabled).toBe(true);
      expect(size.disabled).toBe(true);
      const picker = toolbar.querySelector('.ql-picker') as HTMLElement;
      const label = picker.querySelector('.ql-picker-label') as HTMLElement;
      expect(picker.classList.contains('ql-disabled')).toBe(true);
      expect(picker.getAttribute('aria-disabled')).toBe('true');
      expect(label.getAttribute('aria-disabled')).toBe('true');
      label.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }));
      expect(picker.classList.contains('ql-expanded')).toBe(false);
      bold.dispatchEvent(new MouseEvent('click', { bubbles: true }));
      expect(quillA.getFormat(7, 1).bold).toBeFalsy();

      quillB.setSelection(0, 6);
      expect(bold.disabled).toBe(false);
      expect(size.disabled).toBe(false);
      expect(picker.classList.contains('ql-disabled')).toBe(false);
      expect(bold.classList.contains('ql-active')).toBe(false);
      bold.click();
      expect(quillB.getFormat(0, 1).bold).toBe(true);
      expect(quillA.getFormat(7, 1).bold).toBeFalsy();
    });

    test('binds toolbar controls added later exactly once', () => {
      const { toolbar, quillA, quillB } = setupShared();
      const button = document.createElement('button');
      button.classList.add('ql-bold');
      let bindings = 0;
      const real = button.addEventListener.bind(button);
      button.addEventListener = ((
        type: string,
        listener: EventListenerOrEventListenerObject,
        options?: boolean | AddEventListenerOptions,
      ) => {
        if (type === 'click') bindings += 1;
        return real(type, listener, options);
      }) as typeof button.addEventListener;

      quillA.setSelection(0, 4);
      toolbar.appendChild(button);
      expect(bindings).toBe(1);
      button.remove();
      toolbar.appendChild(button);
      expect(bindings).toBe(1);

      quillB.setSelection(0, 6);
      button.click();
      expect(quillB.getFormat(0, 1).bold).toBe(true);
      expect(quillA.getFormat(0, 1).bold).toBe(true);
      button.click();
      expect(quillB.getFormat(0, 1).bold).toBeFalsy();
    });
  });
});
