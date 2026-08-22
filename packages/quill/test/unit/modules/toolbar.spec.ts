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

  describe('shared toolbar', () => {
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

    const createSharedSetup = () => {
      registerModules();
      const wrapper = document.body.appendChild(document.createElement('div'));
      const toolbar = wrapper.appendChild(document.createElement('div'));
      toolbar.setAttribute('role', 'toolbar');
      addControls(toolbar, [
        ['bold', 'italic'],
        [{ size: ['small', false, 'large'] }],
      ]);
      const editor1 = wrapper.appendChild(document.createElement('div'));
      editor1.innerHTML = normalizeHTML('<p><strong>one</strong></p>');
      const editor2 = wrapper.appendChild(document.createElement('div'));
      editor2.innerHTML = normalizeHTML('<p>two</p>');

      const registry = createRegistry([SizeClass, Bold, Italic, Link]);
      const options = {
        theme: 'snow',
        registry,
        modules: {
          toolbar: { container: toolbar },
        },
      };
      const quill1 = new Quill(editor1, options);
      const quill2 = new Quill(editor2, options);

      const boldButton = toolbar.querySelector(
        'button.ql-bold',
      ) as HTMLButtonElement;

      return { wrapper, toolbar, quill1, quill2, boldButton };
    };

    test('initializes multiple editors with the same container', () => {
      const { toolbar, quill1, quill2 } = createSharedSetup();
      expect((quill1.getModule('toolbar') as Toolbar).container).toBe(toolbar);
      expect((quill2.getModule('toolbar') as Toolbar).container).toBe(toolbar);
      expect(toolbar.querySelectorAll('.ql-picker').length).toBe(1);
      expect(toolbar.querySelectorAll('input.ql-image[type=file]').length).toBe(
        0,
      );
    });

    test('toolbar actions target the most recently focused editor', () => {
      const { quill1, quill2, boldButton } = createSharedSetup();
      quill1.setSelection(1, 1, 'user');
      expect(boldButton.classList.contains('ql-active')).toBe(true);

      quill2.setSelection(0, 3, 'user');
      expect(boldButton.classList.contains('ql-active')).toBe(false);

      boldButton.click();
      expect(quill2.getFormat(0, 3).bold).toBe(true);
      expect(quill1.getFormat(1, 1).bold).toBe(true);
    });

    test('toolbar click does not move focus to a different editor', () => {
      const { quill1, quill2, boldButton } = createSharedSetup();
      quill1.setSelection(1, 1, 'user');
      quill1.focus();

      quill2.setSelection(0, 3, 'user');
      boldButton.click();

      expect(quill2.hasFocus()).toBe(true);
      expect(quill1.hasFocus()).toBe(false);
    });

    test('removing the active editor clears stale toolbar state', () => {
      const { wrapper, quill1, quill2, boldButton } = createSharedSetup();
      quill1.setSelection(1, 1, 'user');
      expect(boldButton.classList.contains('ql-active')).toBe(true);

      quill1.root.parentElement?.remove();
      boldButton.click();
      expect(quill2.getFormat(0, 3).bold).not.toBe(true);

      quill2.setSelection(0, 3, 'user');
      expect(boldButton.classList.contains('ql-active')).toBe(false);
      wrapper.remove();
    });

    test('disabled active editor disables shared controls', () => {
      const { quill1, quill2, boldButton, toolbar } = createSharedSetup();
      const italicButton = toolbar.querySelector(
        'button.ql-italic',
      ) as HTMLButtonElement;
      quill1.setSelection(0, 3, 'user');
      quill1.disable();
      expect(boldButton.disabled).toBe(true);
      expect(italicButton.disabled).toBe(true);
      expect(
        toolbar.querySelector('.ql-picker.ql-size')?.classList.contains(
          'ql-disabled',
        ),
      ).toBe(true);

      italicButton.click();
      expect(quill1.getFormat(0, 3).italic).not.toBe(true);

      quill2.setSelection(0, 3, 'user');
      expect(boldButton.disabled).toBe(false);
      expect(italicButton.disabled).toBe(false);
      expect(
        toolbar.querySelector('.ql-picker.ql-size')?.classList.contains(
          'ql-disabled',
        ),
      ).toBe(false);

      italicButton.click();
      expect(quill2.getFormat(0, 3).italic).toBe(true);
    });

    test('dynamically added controls bind once and target active editor', async () => {
      const { quill1, quill2, toolbar } = createSharedSetup();
      const italicButton = document.createElement('button');
      italicButton.classList.add('ql-italic');
      toolbar.querySelector('.ql-formats')?.appendChild(italicButton);
      await Promise.resolve();

      quill2.setSelection(0, 3, 'user');
      italicButton.click();
      expect(quill2.getFormat(0, 3).italic).toBe(true);
      expect(quill1.getFormat(1, 1).italic).not.toBe(true);

      italicButton.remove();
      await Promise.resolve();
      italicButton.classList.add('ql-italic');
      toolbar.querySelector('.ql-formats')?.appendChild(italicButton);
      await Promise.resolve();
      quill2.formatText(0, 3, { italic: false }, 'api');
      quill2.setSelection(0, 3, 'user');
      italicButton.click();
      expect(quill2.getFormat(0, 3).italic).toBe(true);
    });
  });
});
