import { describe, expect, test, vi } from 'vitest';
import Quill from '../../../src/core/quill.js';
import { Range } from '../../../src/core/selection.js';
import Toolbar, { addControls } from '../../../src/modules/toolbar.js';
import { normalizeHTML, sleep } from '../__helpers__/utils.js';
import BubbleTheme from '../../../src/themes/bubble.js';
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
import Underline from '../../../src/formats/underline.js';
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
    const registerModules = () => {
      Quill.register(
        {
          'themes/snow': SnowTheme,
          'themes/bubble': BubbleTheme,
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

    const setup = () => {
      registerModules();
      const toolbar = createContainer(`
        <button class="ql-bold"></button>
        <button class="ql-italic"></button>
        <select class="ql-size">
          <option value="small"></option>
          <option selected></option>
          <option value="large"></option>
        </select>
        <button class="ql-image"></button>
      `);
      const uploads: [string, Range, File[]][] = [];
      const create = (name: string, html: string, mimetypes: string[]) =>
        new Quill(createContainer(html), {
          theme: 'snow',
          registry: createRegistry([SizeClass, Bold, Italic, Underline]),
          modules: {
            toolbar: { container: toolbar },
            uploader: {
              mimetypes,
              handler(range: Range, files: File[]) {
                uploads.push([name, range, files]);
              },
            },
          },
        });
      const quillA = create(
        'a',
        '<p><strong>0123</strong><span class="ql-size-large">45</span></p>',
        ['image/png', 'image/jpeg'],
      );
      const quillB = create(
        'b',
        '<p>0123<span class="ql-size-small">45</span></p>',
        ['image/gif', 'image/webp'],
      );
      const query = <T extends HTMLElement>(selector: string) =>
        toolbar.querySelector(selector) as T;
      return {
        toolbar,
        quillA,
        quillB,
        uploads,
        query,
        bold: query<HTMLButtonElement>('button.ql-bold'),
        image: query<HTMLButtonElement>('button.ql-image'),
        select: query<HTMLSelectElement>('select.ql-size'),
        picker: query<HTMLElement>('.ql-picker'),
        label: query<HTMLElement>('.ql-picker-label'),
      };
    };

    test('applies actions to the most recently active editor', () => {
      const { quillA, quillB, bold } = setup();
      const contentsA = quillA.getContents();
      quillA.setSelection(0, 2);
      expect(bold.classList.contains('ql-active')).toBe(true);
      quillB.setSelection(0, 2);
      expect(bold.classList.contains('ql-active')).toBe(false);
      expect(bold.getAttribute('aria-pressed')).toBe('false');

      bold.click();
      expect(quillB.getFormat(0, 2)).toEqual({ bold: true });
      expect(quillA.getContents()).toEqual(contentsA);
      expect(quillB.hasFocus()).toBe(true);
      expect(quillA.hasFocus()).toBe(false);
      expect(quillA.getSelection()).toBeNull();
      expect(bold.classList.contains('ql-active')).toBe(true);

      // Reselecting the range A had before must still switch editors
      quillA.setSelection(0, 2);
      quillB.setSelection(2, 2);
      expect(bold.classList.contains('ql-active')).toBe(false);
      quillA.setSelection(0, 2);
      expect(bold.classList.contains('ql-active')).toBe(true);
      bold.click();
      expect(quillA.getFormat(0, 2)).toEqual({});
      expect(quillB.getFormat(0, 2)).toEqual({ bold: true });
      expect(quillB.hasFocus()).toBe(false);
    });

    test('shares pickers and keeps them in sync with the active editor', () => {
      const { toolbar, quillA, quillB, query, select, label } = setup();
      expect(toolbar.querySelectorAll('.ql-picker')).toHaveLength(1);
      expect(toolbar.querySelectorAll('.ql-picker-options')).toHaveLength(1);

      quillA.setSelection(4, 1);
      expect(select.value).toBe('large');
      expect(label.getAttribute('data-value')).toBe('large');
      quillB.setSelection(4, 1);
      expect(select.value).toBe('small');
      expect(label.getAttribute('data-value')).toBe('small');

      query<HTMLElement>('.ql-picker-item[data-value="large"]').click();
      expect(quillB.getFormat(4, 1)).toEqual({ size: 'large' });
      expect(quillA.getFormat(0, 1)).toEqual({ bold: true });
      expect(quillA.getFormat(4, 1)).toEqual({ size: 'large' });
      expect(quillB.hasFocus()).toBe(true);
    });

    test('keeps a single image input matching the active editor', () => {
      const { toolbar, quillA, quillB, uploads, image } = setup();
      const click = vi
        .spyOn(HTMLInputElement.prototype, 'click')
        .mockImplementation(() => {});
      quillA.setSelection(1);
      image.click();
      quillB.setSelection(2);
      image.click();
      const inputs = toolbar.querySelectorAll<HTMLInputElement>(
        'input.ql-image[type=file]',
      );
      expect(inputs).toHaveLength(1);
      expect(click).toHaveBeenCalledTimes(2);
      const [input] = inputs;
      expect(input.getAttribute('accept')).toBe('image/gif, image/webp');

      quillA.setSelection(1);
      expect(input.getAttribute('accept')).toBe('image/png, image/jpeg');
      const file = new File(['x'], 'x.png', { type: 'image/png' });
      const transfer = new DataTransfer();
      transfer.items.add(file);
      input.files = transfer.files;
      input.dispatchEvent(new Event('change'));
      expect(uploads).toEqual([['a', new Range(1, 0), [file]]]);
      click.mockRestore();
    });

    test('forgets a removed active editor', async () => {
      const { toolbar, quillA, quillB, bold, image, label } = setup();
      const click = vi
        .spyOn(HTMLInputElement.prototype, 'click')
        .mockImplementation(() => {});
      quillA.setSelection(4, 1);
      image.click();
      expect(bold.classList.contains('ql-active')).toBe(false);
      quillA.setSelection(0, 2);
      expect(bold.classList.contains('ql-active')).toBe(true);
      const contentsB = quillB.getContents();

      quillA.container.remove();
      await sleep(0);
      const shared = (quillB.getModule('toolbar') as Toolbar).group;
      expect(shared?.active).toBeNull();
      expect(shared?.toolbars).toEqual([quillB.getModule('toolbar')]);
      expect(bold.classList.contains('ql-active')).toBe(false);
      expect(label.hasAttribute('data-value')).toBe(false);
      expect(toolbar.querySelector('input.ql-image[type=file]')).toBeNull();

      bold.click();
      image.click();
      expect(quillB.getContents()).toEqual(contentsB);
      expect(quillB.hasFocus()).toBe(false);
      expect(click).toHaveBeenCalledTimes(1);

      quillB.setSelection(0, 2);
      bold.click();
      expect(quillB.getFormat(0, 2)).toEqual({ bold: true });
      click.mockRestore();
    });

    test('ignores actions for a removed editor before observers run', () => {
      const { quillA, quillB, bold } = setup();
      quillA.setSelection(0, 2);
      const contentsB = quillB.getContents();
      quillA.container.remove();
      bold.click();
      expect(quillB.getContents()).toEqual(contentsB);
      expect(bold.classList.contains('ql-active')).toBe(false);
    });

    test('disables shared controls while the active editor is read-only', () => {
      const { quillA, quillB, query, bold, image, select, picker, label } =
        setup();
      const click = vi
        .spyOn(HTMLInputElement.prototype, 'click')
        .mockImplementation(() => {});
      const contentsB = quillB.getContents();
      quillA.setSelection(0, 2);
      quillB.disable();
      quillB.setSelection(0, 2);
      [bold, image, select].forEach((control) => {
        expect(control.disabled).toBe(true);
      });
      expect(bold.classList.contains('ql-active')).toBe(false);
      expect(picker.classList.contains('ql-disabled')).toBe(true);
      expect(label.getAttribute('aria-disabled')).toBe('true');
      expect(label.tabIndex).toBe(-1);

      label.dispatchEvent(new MouseEvent('mousedown'));
      expect(picker.classList.contains('ql-expanded')).toBe(false);
      query<HTMLElement>('.ql-picker-item[data-value="large"]').click();
      [bold, image].forEach((button) => {
        button.dispatchEvent(new MouseEvent('click', { bubbles: true }));
      });
      select.dispatchEvent(new Event('change'));
      expect(quillB.getContents()).toEqual(contentsB);
      expect(click).not.toHaveBeenCalled();

      quillA.setSelection(0, 2);
      [bold, image, select].forEach((control) => {
        expect(control.disabled).toBe(false);
      });
      expect(bold.classList.contains('ql-active')).toBe(true);
      expect(picker.classList.contains('ql-disabled')).toBe(false);
      expect(label.getAttribute('aria-disabled')).toBe('false');
      expect(label.tabIndex).toBe(0);
      label.dispatchEvent(new MouseEvent('mousedown'));
      expect(picker.classList.contains('ql-expanded')).toBe(true);
      query<HTMLElement>('.ql-picker-item[data-value="small"]').click();
      expect(quillA.getFormat(0, 2)).toEqual({ bold: true, size: 'small' });
      click.mockRestore();
    });

    test('follows the active editor being disabled and enabled', () => {
      const { quillA, bold, select } = setup();
      quillA.setSelection(0, 2);
      quillA.disable();
      expect(bold.disabled).toBe(true);
      expect(select.disabled).toBe(true);
      expect(bold.classList.contains('ql-active')).toBe(false);
      quillA.enable();
      expect(bold.disabled).toBe(false);
      expect(select.disabled).toBe(false);
      expect(bold.classList.contains('ql-active')).toBe(true);
    });

    test('binds buttons added later exactly once', async () => {
      const { toolbar, quillA, quillB } = setup();
      const underline = document.createElement('button');
      underline.classList.add('ql-underline');
      toolbar.appendChild(underline);
      quillB.setSelection(0, 2);
      underline.click();
      expect(quillB.getFormat(0, 2)).toEqual({ underline: true });
      await sleep(0);
      expect(underline.getAttribute('type')).toBe('button');
      expect(underline.classList.contains('ql-active')).toBe(true);
      [quillA, quillB].forEach((quill) => {
        const { controls } = quill.getModule('toolbar') as Toolbar;
        expect(
          controls.filter(([, input]) => input === underline),
        ).toHaveLength(1);
      });

      underline.remove();
      await sleep(0);
      [quillA, quillB].forEach((quill) => {
        const { controls } = quill.getModule('toolbar') as Toolbar;
        expect(controls.some(([, input]) => input === underline)).toBe(false);
      });
      toolbar.appendChild(underline);
      await sleep(0);
      underline.click();
      expect(quillB.getFormat(0, 2)).toEqual({});

      quillA.setSelection(0, 2);
      expect(underline.classList.contains('ql-active')).toBe(false);
      underline.click();
      expect(quillA.getFormat(0, 2)).toEqual({ bold: true, underline: true });
      expect(quillB.getFormat(0, 2)).toEqual({});
      expect(underline.classList.contains('ql-active')).toBe(true);
    });

    test('bubble theme mounts the toolbar in the active editor tooltip', () => {
      registerModules();
      const toolbar = createContainer('<button class="ql-bold"></button>');
      const create = () =>
        new Quill(createContainer('<p>0123</p>'), {
          theme: 'bubble',
          registry: createRegistry([Bold]),
          modules: { toolbar },
        });
      const quillA = create();
      const quillB = create();
      const tooltipOf = (quill: Quill) =>
        (quill.theme as BubbleTheme).tooltip.root;
      expect(toolbar.parentNode).toBe(tooltipOf(quillA));
      quillB.setSelection(0, 2);
      expect(toolbar.parentNode).toBe(tooltipOf(quillB));
      (toolbar.querySelector('button.ql-bold') as HTMLElement).click();
      expect(quillB.getFormat(0, 2)).toEqual({ bold: true });
      expect(quillA.getFormat(0, 2)).toEqual({});
      quillA.setSelection(0, 2);
      expect(toolbar.parentNode).toBe(tooltipOf(quillA));
    });
  });
});
