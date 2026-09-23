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

    const createEditor = (
      html: string,
      toolbar: HTMLElement,
      extra: Record<string, unknown> = {},
    ) => {
      const container = createContainer(html);
      const quill = new Quill(container, {
        theme: 'snow',
        registry: createRegistry([SizeClass, Bold]),
        modules: {
          toolbar: { container: toolbar },
          ...extra,
        },
      });
      return quill;
    };

    test('formats the editor that most recently had focus', () => {
      register();
      const toolbar = createContainer(
        '<span class="ql-formats"><button class="ql-bold"></button><select class="ql-size"></select></span>',
      );
      const first = createEditor('<p>aaaa</p>', toolbar);
      const second = createEditor('<p>bbbb</p>', toolbar);
      const bold = toolbar.querySelector('button.ql-bold') as HTMLButtonElement;
      expect(toolbar.querySelectorAll('.ql-picker')).toHaveLength(1);

      first.setSelection(0, 2, 'user');
      bold.click();
      expect(first.getFormat(0, 2).bold).toBe(true);
      expect(second.getFormat(0, 2).bold).toBeUndefined();
      expect(first.hasFocus()).toBe(true);
      expect(second.hasFocus()).toBe(false);

      second.setSelection(1, 2, 'user');
      expect(bold.classList.contains('ql-active')).toBe(false);
      bold.click();
      expect(second.getFormat(1, 2).bold).toBe(true);
      expect(first.getFormat(0, 2).bold).toBe(true);
      expect(second.hasFocus()).toBe(true);
      expect(first.hasFocus()).toBe(false);
      expect(first.getSelection()).toBe(null);
    });

    test('updates picker state for the active editor', () => {
      register();
      const toolbar = createContainer(
        '<span class="ql-formats"><select class="ql-size"></select></span>',
      );
      const first = createEditor(
        '<p><span class="ql-size-small">aa</span></p>',
        toolbar,
      );
      const second = createEditor(
        '<p><span class="ql-size-huge">bb</span></p>',
        toolbar,
      );
      const picker = toolbar.querySelector('.ql-picker') as HTMLElement;
      first.setSelection(0, 1, 'user');
      expect(
        picker.querySelector('.ql-picker-label')?.getAttribute('data-value'),
      ).toBe('small');
      second.setSelection(0, 1, 'user');
      expect(
        picker.querySelector('.ql-picker-label')?.getAttribute('data-value'),
      ).toBe('huge');
      expect(toolbar.querySelectorAll('.ql-picker')).toHaveLength(1);
    });

    test('keeps one image input pointed at the active editor', () => {
      register();
      const toolbar = createContainer(
        '<span class="ql-formats"><button class="ql-image"></button></span>',
      );
      const first = createEditor('<p>aa</p>', toolbar, {
        uploader: { mimetypes: ['image/gif'] },
      });
      const second = createEditor('<p>bb</p>', toolbar, {
        uploader: { mimetypes: ['image/webp'] },
      });
      const image = toolbar.querySelector(
        'button.ql-image',
      ) as HTMLButtonElement;
      first.setSelection(0, 0, 'user');
      image.click();
      const created = toolbar.querySelector(
        'input.ql-image[type=file]',
      ) as HTMLInputElement;
      const firstAccept = created.getAttribute('accept');
      second.setSelection(0, 0, 'user');
      image.click();
      const inputs = toolbar.querySelectorAll('input.ql-image[type=file]');
      expect(inputs).toHaveLength(1);
      expect(inputs[0].getAttribute('accept')).not.toBe(firstAccept);
      expect(inputs[0].getAttribute('accept')).toContain('image/webp');
      const uploaded: Quill[] = [];
      first.uploader.upload = () => {
        uploaded.push(first);
      };
      second.uploader.upload = () => {
        uploaded.push(second);
      };
      inputs[0].dispatchEvent(new Event('change'));
      expect(uploaded).toEqual([second]);
    });

    test('drops the active editor without leaving toolbar actions behind', () => {
      register();
      const toolbar = createContainer(
        '<span class="ql-formats"><button class="ql-bold"></button><button class="ql-image"></button></span>',
      );
      const first = createEditor('<p>aaaa</p>', toolbar);
      const second = createEditor('<p>bbbb</p>', toolbar);
      const bold = toolbar.querySelector('button.ql-bold') as HTMLButtonElement;
      const image = toolbar.querySelector(
        'button.ql-image',
      ) as HTMLButtonElement;
      first.setSelection(0, 2, 'user');
      image.click();
      expect(toolbar.querySelectorAll('input.ql-image')).toHaveLength(1);
      first.container.remove();
      expect(bold.classList.contains('ql-active')).toBe(false);
      expect(toolbar.querySelectorAll('input.ql-image')).toHaveLength(0);
      bold.click();
      expect(second.getFormat(0, 2).bold).toBeUndefined();
      second.setSelection(0, 2, 'user');
      bold.click();
      expect(second.getFormat(0, 2).bold).toBe(true);
    });

    test('disables shared controls while the active editor is read-only', () => {
      register();
      const toolbar = createContainer(
        '<span class="ql-formats"><button class="ql-bold"></button><select class="ql-size"></select><button class="ql-image"></button></span>',
      );
      const first = createEditor('<p>aaaa</p>', toolbar, {});
      const second = createEditor('<p>bbbb</p>', toolbar);
      const bold = toolbar.querySelector('button.ql-bold') as HTMLButtonElement;
      const select = toolbar.querySelector(
        'select.ql-size',
      ) as HTMLSelectElement;
      const picker = toolbar.querySelector('.ql-picker') as HTMLElement;
      first.setSelection(0, 2, 'user');
      first.disable();
      expect(bold.disabled).toBe(true);
      expect(select.disabled).toBe(true);
      expect(picker.classList.contains('ql-disabled')).toBe(true);
      expect(picker.getAttribute('aria-disabled')).toBe('true');
      expect(picker.hasAttribute('disabled')).toBe(true);
      bold.dispatchEvent(new MouseEvent('click', { bubbles: true }));
      expect(first.getFormat(0, 2).bold).toBeUndefined();
      picker
        .querySelector('.ql-picker-label')
        ?.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }));
      expect(picker.classList.contains('ql-expanded')).toBe(false);
      second.setSelection(0, 2, 'user');
      expect(bold.disabled).toBe(false);
      expect(select.disabled).toBe(false);
      expect(picker.classList.contains('ql-disabled')).toBe(false);
      bold.click();
      expect(second.getFormat(0, 2).bold).toBe(true);
      expect(bold.classList.contains('ql-active')).toBe(true);
    });

    test('binds toolbar buttons added later exactly once', () => {
      register();
      const toolbar = createContainer('<span class="ql-formats"></span>');
      const first = createEditor('<p>aaaa</p>', toolbar);
      const second = createEditor('<p>bbbb</p>', toolbar);
      const group = toolbar.querySelector('.ql-formats') as HTMLElement;
      let firstCalls = 0;
      let secondCalls = 0;
      (first.getModule('toolbar') as Toolbar).addHandler('marker', () => {
        firstCalls += 1;
      });
      (second.getModule('toolbar') as Toolbar).addHandler('marker', () => {
        secondCalls += 1;
      });
      const button = document.createElement('button');
      button.className = 'ql-marker';
      group.appendChild(button);
      first.setSelection(0, 2, 'user');
      button.click();
      button.remove();
      group.appendChild(button);
      button.click();
      expect(firstCalls).toBe(2);
      expect(secondCalls).toBe(0);
      button.remove();
      second.setSelection(0, 2, 'user');
      group.appendChild(button);
      button.click();
      expect(firstCalls).toBe(2);
      expect(secondCalls).toBe(1);
    });
  });
});
