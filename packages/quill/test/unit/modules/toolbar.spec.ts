import { describe, expect, test } from 'vitest';
import Quill from '../../../src/core/quill.js';
import Toolbar, { addControls } from '../../../src/modules/toolbar.js';
import { normalizeHTML } from '../__helpers__/utils.js';
import SnowTheme from '../../../src/themes/snow.js';
import Clipboard from '../../../src/modules/clipboard.js';
import Keyboard from '../../../src/modules/keyboard.js';
import History from '../../../src/modules/history.js';
import Uploader from '../../../src/modules/uploader.js';
import { createRegistry } from '../__helpers__/factory.js';
import Bold from '../../../src/formats/bold.js';
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

  describe('shared container', () => {
    const register = () => {
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

    test('targets the editor that most recently had a selection', () => {
      register();
      const toolbar = document.body.appendChild(document.createElement('div'));
      toolbar.innerHTML =
        '<button type="button" class="ql-bold" aria-pressed="false"></button><select class="ql-size"><option selected="selected"></option><option value="small"></option></select>';
      const a = new Quill(document.body.appendChild(document.createElement('div')), {
        theme: 'snow',
        modules: { toolbar: { container: toolbar } },
        registry: createRegistry([Bold]),
      });
      const b = new Quill(document.body.appendChild(document.createElement('div')), {
        theme: 'snow',
        modules: { toolbar: { container: toolbar } },
        registry: createRegistry([Bold]),
      });
      a.setText('aaaa');
      b.setText('bbbb');
      expect(toolbar.querySelectorAll('.ql-picker').length).toBe(1);
      expect(toolbar.querySelectorAll('input.ql-image')).toHaveLength(0);

      a.setSelection(0, 2);
      const bold = toolbar.querySelector('button.ql-bold') as HTMLButtonElement;
      bold.click();
      expect(a.getFormat(0, 2).bold).toBe(true);
      expect(b.getFormat(0, 2).bold).toBeUndefined();
      expect(bold.classList.contains('ql-active')).toBe(true);

      b.setSelection(0, 2);
      expect(a.getSelection()).toBeNull();
      expect(bold.classList.contains('ql-active')).toBe(false);
      bold.click();
      expect(b.getFormat(0, 2).bold).toBe(true);
      expect(a.getFormat(0, 2).bold).toBe(true);
      expect(a.hasFocus()).toBe(false);
      expect(b.hasFocus()).toBe(true);
    });

    test('disables controls for a read-only active editor', () => {
      register();
      const toolbar = document.body.appendChild(document.createElement('div'));
      toolbar.innerHTML =
        '<button type="button" class="ql-bold" aria-pressed="false"></button><select class="ql-header"></select>';
      const enabled = new Quill(
        document.body.appendChild(document.createElement('div')),
        {
          theme: 'snow',
          modules: { toolbar: { container: toolbar } },
          registry: createRegistry([Bold]),
        },
      );
      const readonly = new Quill(
        document.body.appendChild(document.createElement('div')),
        {
          theme: 'snow',
          readOnly: true,
          modules: { toolbar: { container: toolbar } },
          registry: createRegistry([Bold]),
        },
      );
      enabled.setText('hello');
      readonly.setText('hello');
      readonly.setSelection(0, 2);
      const bold = toolbar.querySelector('button.ql-bold') as HTMLButtonElement;
      expect(bold.disabled).toBe(true);
      const picker = toolbar.querySelector('.ql-picker');
      if (picker) {
        expect(picker.getAttribute('aria-disabled')).toBe('true');
      }
      bold.click();
      expect(readonly.getFormat(0, 2).bold).toBeUndefined();

      enabled.setSelection(0, 2);
      expect(bold.disabled).toBe(false);
      bold.click();
      expect(enabled.getFormat(0, 2).bold).toBe(true);
    });

    test('drops the active editor when it is removed', () => {
      register();
      const toolbar = document.body.appendChild(document.createElement('div'));
      toolbar.innerHTML =
        '<button type="button" class="ql-bold" aria-pressed="false"></button>';
      const a = new Quill(document.body.appendChild(document.createElement('div')), {
        theme: 'snow',
        modules: { toolbar: { container: toolbar } },
        registry: createRegistry([Bold]),
      });
      const b = new Quill(document.body.appendChild(document.createElement('div')), {
        theme: 'snow',
        modules: { toolbar: { container: toolbar } },
        registry: createRegistry([Bold]),
      });
      a.setText('hello');
      b.setText('hello');
      a.setSelection(0, 2);
      a.container.remove();
      const bold = toolbar.querySelector('button.ql-bold') as HTMLButtonElement;
      bold.click();
      expect(b.getFormat(0, 2).bold).toBeUndefined();
      b.setSelection(0, 2);
      bold.click();
      expect(b.getFormat(0, 2).bold).toBe(true);
    });

    test('binds toolbar buttons added later exactly once', () => {
      register();
      const toolbar = document.body.appendChild(document.createElement('div'));
      const editor = new Quill(
        document.body.appendChild(document.createElement('div')),
        {
          theme: 'snow',
          modules: { toolbar: { container: toolbar } },
          registry: createRegistry([Bold]),
        },
      );
      const other = new Quill(
        document.body.appendChild(document.createElement('div')),
        {
          theme: 'snow',
          modules: { toolbar: { container: toolbar } },
          registry: createRegistry([Bold]),
        },
      );
      editor.setText('hello');
      other.setText('hello');
      editor.setSelection(0, 2);
      const button = document.createElement('button');
      button.classList.add('ql-bold');
      toolbar.appendChild(button);
      button.click();
      expect(editor.getFormat(0, 2).bold).toBe(true);
      button.remove();
      toolbar.appendChild(button);
      button.click();
      expect(editor.getFormat(0, 2).bold).toBeUndefined();
      expect(other.getFormat(0, 2).bold).toBeUndefined();
    });
  });
});
