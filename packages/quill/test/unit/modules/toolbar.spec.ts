import { describe, expect, test, vitest } from 'vitest';
import Quill from '../../../src/core/quill.js';
import Toolbar, { addControls } from '../../../src/modules/toolbar.js';
import { normalizeHTML, sleep } from '../__helpers__/utils.js';
import SnowTheme from '../../../src/themes/snow.js';
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
import Image from '../../../src/formats/image.js';
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
    const registerSharedModules = () => {
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

    const setupShared = (
      toolbarHTML = `
        <button type="button" class="ql-bold" aria-pressed="false"></button>
        <button type="button" class="ql-italic" aria-pressed="false"></button>
        <select class="ql-size">
          <option value="small"></option>
          <option selected="selected"></option>
          <option value="large"></option>
        </select>
        <button type="button" class="ql-image" aria-pressed="false"></button>
      `,
    ) => {
      registerSharedModules();
      const toolbar = createContainer(toolbarHTML);
      const editor1 = createContainer('<p>aaaa</p>');
      const editor2 = createContainer('<p>bbbb</p>');
      const options = {
        modules: {
          toolbar: { container: toolbar },
        },
        theme: 'snow' as const,
        registry: createRegistry([
          SizeClass,
          Bold,
          Italic,
          AlignClass,
          Link,
          Image,
        ]),
      };
      const quill1 = new Quill(editor1, options);
      const quill2 = new Quill(editor2, {
        ...options,
        registry: createRegistry([
          SizeClass,
          Bold,
          Italic,
          AlignClass,
          Link,
          Image,
        ]),
      });
      return { toolbar, quill1, quill2 };
    };

    test('does nothing until an editor receives selection or focus', () => {
      const { toolbar, quill1, quill2 } = setupShared();
      const boldButton = toolbar.querySelector(
        'button.ql-bold',
      ) as HTMLButtonElement;
      boldButton.click();
      expect(quill1.getFormat(0, 4)).toEqual({});
      expect(quill2.getFormat(0, 4)).toEqual({});
    });

    test('formats the editor that most recently had a selection', () => {
      const { toolbar, quill1, quill2 } = setupShared();
      const boldButton = toolbar.querySelector(
        'button.ql-bold',
      ) as HTMLButtonElement;
      quill1.setSelection(0, 4);
      boldButton.click();
      expect(quill1.getFormat(0, 4)).toEqual({ bold: true });
      expect(quill2.getFormat(0, 4)).toEqual({});
      quill2.setSelection(0, 4);
      boldButton.click();
      expect(quill2.getFormat(0, 4)).toEqual({ bold: true });
    });

    test('updates active buttons and pickers when switching editors', () => {
      const { toolbar, quill1, quill2 } = setupShared();
      const boldButton = toolbar.querySelector(
        'button.ql-bold',
      ) as HTMLButtonElement;
      const sizeSelect = toolbar.querySelector(
        'select.ql-size',
      ) as HTMLSelectElement;
      quill1.formatText(0, 4, 'bold', true);
      quill1.formatText(0, 4, 'size', 'small');
      quill2.formatText(0, 4, 'size', 'large');
      quill1.setSelection(1);
      expect(boldButton.classList.contains('ql-active')).toBe(true);
      expect(sizeSelect.selectedIndex).toEqual(0);
      expect(
        toolbar.querySelector('.ql-picker-label')?.getAttribute('data-value'),
      ).toEqual('small');
      quill2.setSelection(1);
      expect(boldButton.classList.contains('ql-active')).toBe(false);
      expect(sizeSelect.selectedIndex).toEqual(2);
      expect(
        toolbar.querySelector('.ql-picker-label')?.getAttribute('data-value'),
      ).toEqual('large');
    });

    test('does not move the caret into another editor', () => {
      const { toolbar, quill1, quill2 } = setupShared();
      const boldButton = toolbar.querySelector(
        'button.ql-bold',
      ) as HTMLButtonElement;
      quill1.setSelection(0, 2);
      quill2.setSelection(1, 2);
      boldButton.click();
      expect(quill1.getSelection()).toBe(null);
      expect(quill2.getSelection()).toEqual({ index: 1, length: 2 });
      expect(quill1.getFormat(0, 4)).toEqual({});
      expect(quill2.getFormat(1, 2)).toEqual({ bold: true });
    });

    test('does not duplicate theme-managed UI', () => {
      const { toolbar, quill1 } = setupShared();
      expect(toolbar.querySelectorAll('.ql-picker').length).toEqual(1);
      expect(
        toolbar.querySelectorAll('input.ql-image[type=file]').length,
      ).toEqual(0);
      const imageButton = toolbar.querySelector(
        'button.ql-image',
      ) as HTMLButtonElement;
      const inputClick = vitest
        .spyOn(HTMLInputElement.prototype, 'click')
        .mockImplementation(() => {});
      quill1.setSelection(0, 1);
      imageButton.click();
      expect(
        toolbar.querySelectorAll('input.ql-image[type=file]').length,
      ).toEqual(1);
      imageButton.click();
      expect(
        toolbar.querySelectorAll('input.ql-image[type=file]').length,
      ).toEqual(1);
      inputClick.mockRestore();
    });

    test('routes the hidden image input to the active editor', () => {
      const { toolbar, quill1, quill2 } = setupShared();
      const imageButton = toolbar.querySelector(
        'button.ql-image',
      ) as HTMLButtonElement;
      const upload1 = vitest.spyOn(quill1.uploader, 'upload');
      const upload2 = vitest.spyOn(quill2.uploader, 'upload');
      const inputClick = vitest
        .spyOn(HTMLInputElement.prototype, 'click')
        .mockImplementation(() => {});
      quill1.setSelection(1);
      imageButton.click();
      inputClick.mockRestore();
      const fileInput = toolbar.querySelector(
        'input.ql-image[type=file]',
      ) as HTMLInputElement;
      const file = new File(['x'], 'one.png', { type: 'image/png' });
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      });
      fileInput.dispatchEvent(new Event('change'));
      expect(upload1).toHaveBeenCalledTimes(1);
      expect(upload2).not.toHaveBeenCalled();
      upload1.mockClear();
      quill2.setSelection(2);
      expect(fileInput.getAttribute('accept')).toContain('image/png');
      fileInput.dispatchEvent(new Event('change'));
      expect(upload2).toHaveBeenCalledTimes(1);
      expect(upload1).not.toHaveBeenCalled();
    });

    test('clears active state when the active editor is removed', async () => {
      const { toolbar, quill1, quill2 } = setupShared();
      const boldButton = toolbar.querySelector(
        'button.ql-bold',
      ) as HTMLButtonElement;
      quill1.formatText(0, 4, 'bold', true);
      quill1.setSelection(1);
      expect(boldButton.classList.contains('ql-active')).toBe(true);
      const upload1 = vitest.spyOn(quill1.uploader, 'upload');
      const upload2 = vitest.spyOn(quill2.uploader, 'upload');
      const inputClick = vitest
        .spyOn(HTMLInputElement.prototype, 'click')
        .mockImplementation(() => {});
      (toolbar.querySelector('button.ql-image') as HTMLButtonElement).click();
      inputClick.mockRestore();
      quill1.container.remove();
      await sleep(0);
      expect(boldButton.classList.contains('ql-active')).toBe(false);
      boldButton.click();
      expect(quill2.getFormat(0, 4)).toEqual({});
      const fileInput = toolbar.querySelector(
        'input.ql-image[type=file]',
      ) as HTMLInputElement | null;
      fileInput?.dispatchEvent(new Event('change'));
      expect(upload1).not.toHaveBeenCalled();
      expect(upload2).not.toHaveBeenCalled();
      quill2.setSelection(1);
      boldButton.click();
      expect(quill2.getFormat()).toEqual({ bold: true });
    });

    test('destroying the active editor leaves remaining editors idle until focused', () => {
      const { toolbar, quill1, quill2 } = setupShared();
      const boldButton = toolbar.querySelector(
        'button.ql-bold',
      ) as HTMLButtonElement;
      quill1.setSelection(0, 4);
      quill1.destroy();
      boldButton.click();
      expect(quill2.getFormat(0, 4)).toEqual({});
      quill2.setSelection(0, 4);
      boldButton.click();
      expect(quill2.getFormat(0, 4)).toEqual({ bold: true });
    });

    test('disables shared controls when the active editor is read-only', () => {
      const { toolbar, quill1, quill2 } = setupShared();
      const boldButton = toolbar.querySelector(
        'button.ql-bold',
      ) as HTMLButtonElement;
      const sizeSelect = toolbar.querySelector(
        'select.ql-size',
      ) as HTMLSelectElement;
      const imageButton = toolbar.querySelector(
        'button.ql-image',
      ) as HTMLButtonElement;
      quill1.setSelection(0, 4);
      quill1.disable();
      expect(boldButton.disabled).toBe(true);
      expect(sizeSelect.disabled).toBe(true);
      expect(
        toolbar.querySelector('.ql-picker')?.classList.contains('ql-disabled'),
      ).toBe(true);
      expect(
        toolbar.querySelector('.ql-picker')?.getAttribute('aria-disabled'),
      ).toBe('true');
      boldButton.click();
      expect(quill1.getFormat()).toEqual({});
      imageButton.click();
      expect(
        toolbar.querySelectorAll('input.ql-image[type=file]').length,
      ).toEqual(0);
      quill2.setSelection(0, 4);
      expect(boldButton.disabled).toBe(false);
      expect(sizeSelect.disabled).toBe(false);
      expect(
        toolbar.querySelector('.ql-picker')?.classList.contains('ql-disabled'),
      ).toBe(false);
      boldButton.click();
      expect(quill2.getFormat(0, 4)).toEqual({ bold: true });
      expect(quill1.getFormat(0, 4)).toEqual({});
    });

    test('binds dynamically added buttons once to the active editor', async () => {
      const { toolbar, quill1, quill2 } = setupShared(`
        <button type="button" class="ql-italic" aria-pressed="false"></button>
      `);
      const boldButton = document.createElement('button');
      boldButton.classList.add('ql-bold');
      toolbar.appendChild(boldButton);
      await sleep(0);
      quill2.setSelection(0, 4);
      boldButton.click();
      expect(quill2.getFormat(0, 4)).toEqual({ bold: true });
      expect(quill1.getFormat(0, 4)).toEqual({});
      quill2.formatText(0, 4, 'bold', false);
      boldButton.remove();
      await sleep(0);
      toolbar.appendChild(boldButton);
      await sleep(0);
      quill2.setSelection(0, 4);
      quill2.format('bold', true);
      expect(boldButton.classList.contains('ql-active')).toBe(true);
      boldButton.click();
      expect(quill2.getFormat(0, 4)).toEqual({});
    });
  });
});
