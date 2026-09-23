import { afterEach, describe, expect, it } from 'vitest';
import Window from '../../src/window/Window.js';
import Browser from '../../src/browser/Browser.js';
import DOMExceptionNameEnum from '../../src/exception/DOMExceptionNameEnum.js';

describe('Body consumption abort on shutdown', () => {
	const windows: Window[] = [];
	const browsers: Browser[] = [];

	afterEach(async () => {
		await Promise.all(windows.splice(0).map((window) => window.happyDOM.close()));
		await Promise.all(browsers.splice(0).map((browser) => browser.close()));
	});

	function openWindow(): Window {
		const window = new Window({ url: 'https://example.com/' });
		windows.push(window);
		return window;
	}

	async function expectAbortError(pending: Promise<unknown>): Promise<void> {
		await expect(pending).rejects.toMatchObject({
			name: DOMExceptionNameEnum.abortError
		});
	}

	it('rejects an in-flight Response body read with AbortError when the window is closed', async () => {
		const window = openWindow();
		const stream = new window.ReadableStream({
			start() {}
		});
		const response = new window.Response(stream);
		const pending = response.text();
		const assertion = expectAbortError(pending);

		await window.happyDOM.close();
		await assertion;
	});

	it('rejects an in-flight Request body read with AbortError when the window is closed', async () => {
		const window = openWindow();
		const stream = new window.ReadableStream({
			start() {}
		});
		const request = new window.Request('https://example.com/submit', {
			method: 'POST',
			body: stream
		});
		const pending = request.text();
		const assertion = expectAbortError(pending);

		await window.happyDOM.close();
		await assertion;
	});

	it('rejects multipart formData() parsing with AbortError when the page is closed', async () => {
		const browser = new Browser();
		browsers.push(browser);
		const page = browser.newPage();
		const stream = new page.mainFrame.window.ReadableStream({
			start() {}
		});
		const response = new page.mainFrame.window.Response(stream, {
			headers: { 'Content-Type': 'multipart/form-data; boundary=----boundary' }
		});
		const pending = response.formData();
		const assertion = expectAbortError(pending);

		await page.close();
		await assertion;
	});

	it('rejects multipart Request formData() parsing with AbortError when the browser is closed', async () => {
		const browser = new Browser();
		browsers.push(browser);
		const page = browser.newPage();
		const stream = new page.mainFrame.window.ReadableStream({
			start() {}
		});
		const request = new page.mainFrame.window.Request('https://example.com/submit', {
			method: 'POST',
			body: stream,
			headers: { 'Content-Type': 'multipart/form-data; boundary=----boundary' }
		});
		const pending = request.formData();
		const assertion = expectAbortError(pending);

		await browser.close();
		await assertion;
	});

	it('rejects an in-flight body read with AbortError when the browser is closed', async () => {
		const browser = new Browser();
		browsers.push(browser);
		const page = browser.newPage();
		const stream = new page.mainFrame.window.ReadableStream({
			start() {}
		});
		const response = new page.mainFrame.window.Response(stream);
		const pending = response.arrayBuffer();
		const assertion = expectAbortError(pending);

		await browser.close();
		await assertion;
	});

	it('rejects an in-flight body read with AbortError when navigation discards the page', async () => {
		const browser = new Browser();
		browsers.push(browser);
		const page = browser.newPage();
		const stream = new page.mainFrame.window.ReadableStream({
			start() {}
		});
		const response = new page.mainFrame.window.Response(stream);
		const pending = response.text();
		const assertion = expectAbortError(pending);

		await page.goto('about:blank');
		await assertion;
	});

	it('keeps a fully buffered Response readable after shutdown', async () => {
		const window = openWindow();
		const response = new window.Response('still here');
		const formData = new window.FormData();
		formData.append('field', 'value');
		const multipart = new window.Response(formData);

		await window.happyDOM.close();

		await expect(response.text()).resolves.toBe('still here');
		expect(response.bodyUsed).toBe(true);
		await expect((await multipart.formData()).get('field')).toBe('value');
	});

	it('returns an uninterrupted body read unchanged', async () => {
		const window = openWindow();
		const response = new window.Response('hello');

		await expect(response.text()).resolves.toBe('hello');
	});

	it('does not run timers or animation frames from a discarded page', async () => {
		const window = openWindow();
		let timedOut = false;
		let animated = false;

		window.setTimeout(() => {
			timedOut = true;
		}, 0);
		window.setInterval(() => {
			timedOut = true;
		}, 0);
		window.requestAnimationFrame(() => {
			animated = true;
		});

		await window.happyDOM.close();
		await new Promise((resolve) => setTimeout(resolve, 20));

		expect(timedOut).toBe(false);
		expect(animated).toBe(false);
	});

	it('does not run timers or animation frames after navigation swaps the page', async () => {
		const browser = new Browser();
		browsers.push(browser);
		const page = browser.newPage();
		const discardedWindow = page.mainFrame.window;
		let timedOut = false;
		let animated = false;

		discardedWindow.setTimeout(() => {
			timedOut = true;
		}, 0);
		discardedWindow.requestAnimationFrame(() => {
			animated = true;
		});

		await page.goto('about:blank');
		await new Promise((resolve) => setTimeout(resolve, 20));

		expect(timedOut).toBe(false);
		expect(animated).toBe(false);
	});
});
