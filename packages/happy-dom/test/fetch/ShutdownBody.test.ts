import { ReadableStream } from 'stream/web';
import Browser from '../../src/browser/Browser.js';
import DOMExceptionNameEnum from '../../src/exception/DOMExceptionNameEnum.js';
import Window from '../../src/window/Window.js';
import { describe, it, expect } from 'vitest';

describe('Shutdown body reads and timers', () => {
	it('rejects an in-flight response body read with AbortError when the window closes', async () => {
		const window = new Window();
		let pull: ((controller: ReadableStreamDefaultController<string>) => void) | null = null;
		const response = new window.Response(
			new ReadableStream({
				start(controller) {
					pull = () => controller.enqueue('chunk');
				}
			})
		);
		const pending = response.text();
		await new Promise((resolve) => setImmediate(resolve));
		const closePromise = window.happyDOM.close();
		await expect(pending).rejects.toMatchObject({ name: DOMExceptionNameEnum.abortError });
		await closePromise;
	});

	it('rejects an in-flight request body read with AbortError when the page closes', async () => {
		const window = new Window();
		const request = new window.Request('https://example.com/', {
			method: 'POST',
			body: new ReadableStream({
				start() {}
			}),
			duplex: 'half'
		} as RequestInit);
		const pending = request.text();
		await new Promise((resolve) => setImmediate(resolve));
		const closePromise = window.happyDOM.close();
		await expect(pending).rejects.toMatchObject({ name: DOMExceptionNameEnum.abortError });
		await closePromise;
	});

	it('rejects multipart formData parsing with AbortError when the browser closes', async () => {
		const window = new Window();
		const response = new window.Response(
			new ReadableStream({
				start() {}
			}),
			{ headers: { 'Content-Type': 'multipart/form-data; boundary=----boundary' } }
		);
		const pending = response.formData();
		await new Promise((resolve) => setImmediate(resolve));
		const browser = window.happyDOM.settings && (window as any);
		const closePromise = window.happyDOM.close();
		await expect(pending).rejects.toMatchObject({ name: DOMExceptionNameEnum.abortError });
		await closePromise;
		expect(browser).toBeTruthy();
	});

	it('keeps a fully buffered response readable after close', async () => {
		const window = new Window();
		const response = new window.Response('hello buffered');
		await window.happyDOM.close();
		await expect(response.text()).resolves.toBe('hello buffered');
	});

	it('clears timers from page state discarded by navigation', async () => {
		const browser = new Browser();
		const page = browser.newPage();
		let timeoutRan = false;
		let frameRan = false;
		page.mainFrame.window.setTimeout(() => {
			timeoutRan = true;
		}, 30);
		page.mainFrame.window.requestAnimationFrame(() => {
			frameRan = true;
		});
		const navigation = page.goto('about:blank');
		await new Promise((resolve) => setTimeout(resolve, 50));
		expect(timeoutRan).toBe(false);
		expect(frameRan).toBe(false);
		await navigation.catch(() => undefined);
		await browser.close();
	});

	it('does not run timers or animation frames from a discarded page', async () => {
		const window = new Window();
		let timeoutRan = false;
		let frameRan = false;
		window.setTimeout(() => {
			timeoutRan = true;
		}, 50);
		window.requestAnimationFrame(() => {
			frameRan = true;
		});
		await window.happyDOM.close();
		await new Promise((resolve) => setTimeout(resolve, 80));
		expect(timeoutRan).toBe(false);
		expect(frameRan).toBe(false);
	});
});
