import { describe, expect, test } from 'vitest';
import Quill from '../../../src/core/quill.js';
import Toolbar, { addControls } from '../../../src/modules/toolbar.js';
import { normalizeHTML } from '../__helpers__/utils.js';
import SnowTheme from '../../../src/themes/snow.js';
import BubbleTheme from '../../../src/themes/bubble.js';
import Clipboard from '../../../src/modules/clipboard.js';
import Keyboard from '../../../src/modules/keyboard.js';
import History from '../../../src/modules/history.js';
import Uploader from '../../../src/modules/uploader.js';
import { createRegistry } from '../__helpers__/factory.js';
import Input from '../../../src/modules/input.js';
import { SizeClass } from '../../../src/formats/size.js';
import Bold from '../../../src/formats/bold.js';
import Italic from '../../../src/formats/italic.js';
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

  describe('shared container', () => {
    const tick = () => new Promise((resolve) => setTimeout(resolve, 0));

    const setup = (
      editorOptions: { readOnly?: boolean; mimetypes?: string[] }[] = [{}, {}],
    ) => {
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
      const toolbar = createContainer(`
        <button class="ql-bold"></button>
        <button class="ql-italic"></button>
        <button class="ql-image"></button>
        <select class="ql-size">
          <option value="small"></option>
          <option selected></option>
          <option value="large"></option>
        </select>
      `);
      const quills = editorOptions.map(({ readOnly, mimetypes }, i) => {
        const container = createContainer(
          `<p>${i}ab <strong>cd</strong></p><p><span class="ql-size-large">ef</span></p>`,
        );
        return new Quill(container, {
          modules: {
            toolbar: { container: toolbar },
            uploader: mimetypes ? { mimetypes } : true,
          },
          readOnly,
          theme: 'snow',
          registry: createRegistry([SizeClass, Bold, Italic]),
        });
      });
      const bold = toolbar.querySelector('button.ql-bold') as HTMLButtonElement;
      const size = toolbar.querySelector('select.ql-size') as HTMLSelectElement;
      return { toolbar, quills, bold, size };
    };

    test('applies actions to the most recently selected editor', () => {
      const {
        quills: [first, second],
        bold,
      } = setup();
      first.setSelection(0, 3);
      bold.click();
      expect(first.getContents().ops[0]).toEqual({
        insert: '0ab',
        attributes: { bold: true },
      });
      second.setSelection(0, 3);
      bold.click();
      expect(second.getContents().ops[0]).toEqual({
        insert: '1ab',
        attributes: { bold: true },
      });
      expect(first.getText(0, 6)).toEqual('0ab cd');
      expect(second.hasFocus()).toBe(true);
      expect(first.hasFocus()).toBe(false);
      expect(first.getSelection()).toBeNull();
    });

    test('updates button and picker state when switching editors', () => {
      const {
        toolbar,
        quills: [first, second],
        bold,
        size,
      } = setup();
      first.setSelection(5);
      expect(bold.classList.contains('ql-active')).toBe(true);
      second.setSelection(2);
      expect(bold.classList.contains('ql-active')).toBe(false);
      second.setSelection(8);
      const label = toolbar.querySelector('.ql-size .ql-picker-label');
      expect(size.selectedIndex).toEqual(2);
      expect(label?.getAttribute('data-value')).toEqual('large');
      first.setSelection(2);
      expect(size.selectedIndex).toEqual(1);
      expect(label?.hasAttribute('data-value')).toBe(false);
    });

    test('does not duplicate theme UI', () => {
      const { toolbar } = setup([{}, {}, {}]);
      expect(toolbar.querySelectorAll('.ql-picker').length).toEqual(1);
      expect(toolbar.querySelectorAll('select.ql-size').length).toEqual(1);
    });

    test('image input follows the active editor', () => {
      const {
        toolbar,
        quills: [first, second],
      } = setup([{ mimetypes: ['image/png'] }, { mimetypes: ['image/gif'] }]);
      const imageButton = toolbar.querySelector(
        'button.ql-image',
      ) as HTMLButtonElement;
      first.setSelection(1);
      imageButton.click();
      second.setSelection(1);
      imageButton.click();
      const inputs = toolbar.querySelectorAll('input.ql-image[type=file]');
      expect(inputs.length).toEqual(1);
      expect(inputs[0].getAttribute('accept')).toEqual('image/gif, image/jpeg');
      first.setSelection(1);
      expect(inputs[0].getAttribute('accept')).toEqual('image/png, image/jpeg');
    });

    test('removing the active editor', async () => {
      const {
        toolbar,
        quills: [first, second],
        bold,
      } = setup();
      const imageButton = toolbar.querySelector(
        'button.ql-image',
      ) as HTMLButtonElement;
      second.setSelection(0, 3);
      imageButton.click();
      expect(bold.classList.contains('ql-active')).toBe(false);
      second.setSelection(4, 2);
      expect(bold.classList.contains('ql-active')).toBe(true);
      second.container.remove();
      await tick();
      expect(bold.classList.contains('ql-active')).toBe(false);
      expect(toolbar.querySelector('input.ql-image[type=file]')).toBeNull();
      const before = first.getContents();
      bold.click();
      expect(first.getContents()).toEqual(before);
      first.setSelection(0, 3);
      bold.click();
      expect(first.getContents().ops[0]).toEqual({
        insert: '0ab',
        attributes: { bold: true },
      });
    });

    test('disables controls while the active editor is disabled', () => {
      const {
        toolbar,
        quills: [first, second],
        bold,
        size,
      } = setup([{}, { readOnly: true }]);
      const picker = toolbar.querySelector('.ql-picker.ql-size') as HTMLElement;
      const label = picker.querySelector('.ql-picker-label') as HTMLElement;
      first.setSelection(0, 3);
      expect(bold.disabled).toBe(false);
      second.setSelection(0, 3);
      expect(bold.disabled).toBe(true);
      expect(size.disabled).toBe(true);
      expect(picker.classList.contains('ql-disabled')).toBe(true);
      expect(label.getAttribute('aria-disabled')).toEqual('true');
      label.dispatchEvent(new MouseEvent('mousedown'));
      expect(picker.classList.contains('ql-expanded')).toBe(false);
      const before = second.getContents();
      bold.dispatchEvent(new MouseEvent('click'));
      (picker.querySelector('[data-value="small"]') as HTMLElement).click();
      expect(second.getContents()).toEqual(before);

      first.setSelection(0, 3);
      expect(bold.disabled).toBe(false);
      expect(size.disabled).toBe(false);
      expect(picker.classList.contains('ql-disabled')).toBe(false);
      expect(label.hasAttribute('aria-disabled')).toBe(false);
      bold.click();
      expect(bold.classList.contains('ql-active')).toBe(true);

      first.disable();
      expect(bold.disabled).toBe(true);
      expect(picker.classList.contains('ql-disabled')).toBe(true);
      first.enable();
      expect(bold.disabled).toBe(false);
      expect(picker.classList.contains('ql-disabled')).toBe(false);
    });

    test('bubble tooltip hosts the toolbar of the active editor', () => {
      Quill.register({ 'themes/bubble': BubbleTheme }, true);
      const toolbar = createContainer('<button class="ql-bold"></button>');
      const [first, second] = [0, 1].map(
        (i) =>
          new Quill(createContainer(`<p>${i}ab</p>`), {
            modules: { toolbar: { container: toolbar } },
            theme: 'bubble',
            registry: createRegistry([Bold]),
          }),
      );
      expect(first.container.contains(toolbar)).toBe(true);
      second.setSelection(0, 2);
      expect(second.container.contains(toolbar)).toBe(true);
      first.setSelection(0, 2);
      expect(first.container.contains(toolbar)).toBe(true);
    });

    test('binds controls added after initialization once', async () => {
      const {
        toolbar,
        quills: [first, second],
        bold,
      } = setup();
      const italic = document.createElement('button');
      italic.classList.add('ql-italic');
      toolbar.appendChild(italic);
      await tick();
      second.setSelection(0, 3);
      italic.click();
      expect(second.getContents().ops[0]).toEqual({
        insert: '1ab',
        attributes: { italic: true },
      });
      expect(first.getText(0, 3)).toEqual('0ab');

      bold.remove();
      await tick();
      toolbar.appendChild(bold);
      await tick();
      second.setSelection(0, 3);
      bold.click();
      expect(second.getContents().ops[0]).toEqual({
        insert: '1ab',
        attributes: { italic: true, bold: true },
      });
      expect(bold.classList.contains('ql-active')).toBe(true);
    });
  });
});
