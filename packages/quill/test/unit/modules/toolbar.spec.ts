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
import Image from '../../../src/formats/image.js';

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

    const setup = (count = 2) => {
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
        <button class="ql-image"></button>
        <select class="ql-size">
          <option value="small"></option>
          <option selected></option>
          <option value="large"></option>
        </select>
      `);
      const editors = Array.from({ length: count }, (_, i) => {
        const container = createContainer(
          `<p>editor${i}</p><p><strong>bold${i}</strong></p>`,
        );
        return new Quill(container, {
          modules: { toolbar: { container: toolbar } },
          theme: 'snow',
          registry: createRegistry([SizeClass, Bold, Image]),
        });
      });
      const bold = toolbar.querySelector('button.ql-bold') as HTMLButtonElement;
      const image = toolbar.querySelector(
        'button.ql-image',
      ) as HTMLButtonElement;
      const size = toolbar.querySelector('select.ql-size') as HTMLSelectElement;
      return { toolbar, editors, bold, image, size };
    };

    test('does not duplicate theme UI', () => {
      const { toolbar, image, editors } = setup(3);
      expect(toolbar.querySelectorAll('.ql-picker').length).toBe(1);
      editors[1].setSelection(0, 1);
      image.click();
      editors[2].setSelection(0, 1);
      image.click();
      expect(toolbar.querySelectorAll('input.ql-image[type=file]').length).toBe(
        1,
      );
    });

    test('applies actions to the last selected editor only', () => {
      const {
        bold,
        editors: [first, second],
      } = setup();
      second.setSelection(0, 2);
      first.setSelection(0, 2);
      bold.click();
      expect(first.getContents().ops[0]).toEqual({
        insert: 'ed',
        attributes: { bold: true },
      });
      expect(second.getContents().ops[0]).toEqual({ insert: 'editor1\n' });
      expect(first.hasFocus()).toBe(true);
      expect(second.hasFocus()).toBe(false);

      second.setSelection(1, 2);
      bold.click();
      expect(second.getContents().ops[0]).toEqual({ insert: 'e' });
      expect(second.getContents().ops[1]).toEqual({
        insert: 'di',
        attributes: { bold: true },
      });
      expect(second.hasFocus()).toBe(true);
      expect(first.hasFocus()).toBe(false);
    });

    test('active state follows the active editor', () => {
      const {
        bold,
        size,
        editors: [first, second],
      } = setup();
      first.setSelection(10);
      expect(bold.classList.contains('ql-active')).toBe(true);
      second.setSelection(1);
      expect(bold.classList.contains('ql-active')).toBe(false);
      expect(size.selectedIndex).toBe(1);
      first.setSelection(1);
      first.formatText(0, 3, 'bold', true);
      expect(bold.classList.contains('ql-active')).toBe(true);
      second.formatText(0, 3, 'size', 'large');
      expect(size.selectedIndex).toBe(1);
      second.setSelection(1);
      expect(bold.classList.contains('ql-active')).toBe(false);
      expect(size.selectedIndex).toBe(2);
    });

    test('removing the active editor', async () => {
      const {
        bold,
        editors: [first, second],
      } = setup();
      second.setSelection(9, 2);
      expect(bold.classList.contains('ql-active')).toBe(true);
      second.container.remove();
      await tick();
      expect(bold.classList.contains('ql-active')).toBe(false);
      bold.click();
      expect(first.getContents().ops).toEqual([
        { insert: 'editor0\n' },
        { insert: 'bold0', attributes: { bold: true } },
        { insert: '\n' },
      ]);
      expect(first.hasFocus()).toBe(false);
      first.setSelection(0, 2);
      bold.click();
      expect(first.getContents().ops[0]).toEqual({
        insert: 'ed',
        attributes: { bold: true },
      });
    });

    test('disabled active editor', () => {
      const {
        toolbar,
        bold,
        size,
        editors: [first, second],
      } = setup();
      const picker = toolbar.querySelector('.ql-picker') as HTMLElement;
      const label = picker.querySelector('.ql-picker-label') as HTMLElement;
      second.setSelection(0, 2);
      second.disable();
      expect(bold.disabled).toBe(true);
      expect(size.disabled).toBe(true);
      expect(picker.classList.contains('ql-disabled')).toBe(true);
      expect(label.getAttribute('aria-disabled')).toBe('true');
      label.dispatchEvent(new MouseEvent('mousedown'));
      expect(picker.classList.contains('ql-expanded')).toBe(false);
      bold.dispatchEvent(new MouseEvent('click'));
      (picker.querySelector('.ql-picker-item') as HTMLElement).click();
      expect(second.getContents().ops).toEqual([
        { insert: 'editor1\n' },
        { insert: 'bold1', attributes: { bold: true } },
        { insert: '\n' },
      ]);

      first.setSelection(0, 2);
      expect(bold.disabled).toBe(false);
      expect(size.disabled).toBe(false);
      expect(picker.classList.contains('ql-disabled')).toBe(false);
      expect(label.hasAttribute('aria-disabled')).toBe(false);
      bold.click();
      expect(first.getContents().ops[0]).toEqual({
        insert: 'ed',
        attributes: { bold: true },
      });
    });

    test('dynamically added buttons', async () => {
      const {
        toolbar,
        editors: [first, second],
      } = setup();
      const extraBold = document.createElement('button');
      extraBold.classList.add('ql-bold');
      toolbar.appendChild(extraBold);
      await tick();
      expect(extraBold.getAttribute('type')).toBe('button');
      second.setSelection(0, 2);
      extraBold.click();
      expect(second.getContents().ops[0]).toEqual({
        insert: 'ed',
        attributes: { bold: true },
      });
      expect(first.getContents().ops[0]).toEqual({ insert: 'editor0\n' });

      extraBold.remove();
      await tick();
      toolbar.appendChild(extraBold);
      await tick();
      second.setSelection(2, 2);
      const format = vi.spyOn(second, 'format');
      extraBold.click();
      expect(format).toHaveBeenCalledTimes(1);
      expect(second.getContents().ops.slice(0, 2)).toEqual([
        { insert: 'edit', attributes: { bold: true } },
        { insert: 'or1\n' },
      ]);
    });
  });
});
