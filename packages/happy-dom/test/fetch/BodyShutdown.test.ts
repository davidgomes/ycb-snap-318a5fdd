import Browser from '../../src/browser/Browser.js';
import DOMException from '../../src/exception/DOMException.js';
import DOMExceptionNameEnum from '../../src/exception/DOMExceptionNameEnum.js';
import Window from '../../src/window/Window.js';
import { ReadableStream } from 'stream/web';
import { describe, it, expect } from 'vitest';

const TEST_URL = 'https://example.com/';

function createHangingStream(): ReadableStream<Uint8Array> {
	return new ReadableStream({
		start() {}
	});
}

function expectAbortError(promise: Promise<unknown>): Promise<void> {
	return promise.then(
		() => {
			throw new Error('Expected body read to reject with AbortError.');
		},
		(error: Error) => {
			expect(error).toBeInstanceOf(DOMException);
			expect(error.name).toBe(DOMExceptionNameEnum.abortError);
		}
	);
}

describe('Body shutdown', () => {
	describe('happyDOM.close()', () => {
		it('Rejects an in-flight Response body read with AbortError.', async () => {
			const window = new Window();
			const response = new window.Response(createHangingStream());
			const readPromise = expectAbortError(response.text());

			await window.happyDOM?.close();
			await readPromise;
		});

		it('Rejects an in-flight Request body read with AbortError.', async () => {
			const window = new Window();
			const request = new window.Request(TEST_URL, {
				method: 'POST',
				body: createHangingStream()
			});
			const readPromise = expectAbortError(request.arrayBuffer());

			await window.happyDOM?.close();
			await readPromise;
		});

		it('Rejects an in-flight multipart Response.formData() parse with AbortError.', async () => {
			const window = new Window();
			const response = new window.Response(createHangingStream(), {
				headers: {
					'Content-Type': 'multipart/form-data; boundary=----HappyDOM'
				}
			});
			const readPromise = expectAbortError(response.formData());

			await window.happyDOM?.close();
			await readPromise;
		});

		it('Rejects an in-flight multipart Request.formData() parse with AbortError.', async () => {
			const window = new Window();
			const request = new window.Request(TEST_URL, {
				method: 'POST',
				headers: {
					'Content-Type': 'multipart/form-data; boundary=----HappyDOM'
				},
				body: createHangingStream()
			});
			const readPromise = expectAbortError(request.formData());

			await window.happyDOM?.close();
			await readPromise;
		});

		it('Leaves successful Response reads unchanged.', async () => {
			const window = new Window();
			const response = new window.Response('Hello World');
			const text = await response.text();

			await window.happyDOM?.close();

			expect(text).toBe('Hello World');
		});

		it('Keeps fully buffered Response bodies readable after shutdown.', async () => {
			const window = new Window();
			const response = new window.Response('Hello World');

			await window.happyDOM?.close();

			expect(await response.text()).toBe('Hello World');
		});

		it('Keeps fully buffered multipart Response.formData() readable after shutdown.', async () => {
			const window = new Window();
			const formData = new window.FormData();
			formData.append('key', 'value');
			const response = new window.Response(formData);

			await window.happyDOM?.close();

			const parsed = await response.formData();
			expect(parsed.get('key')).toBe('value');
		});

		it('Clears scheduled timers and requestAnimationFrame callbacks.', async () => {
			const window = new Window();
			let timeoutFired = false;
			let animationFrameFired = false;

			window.setTimeout(() => {
				timeoutFired = true;
			}, 20);
			window.requestAnimationFrame(() => {
				animationFrameFired = true;
			});

			await window.happyDOM?.close();
			await new Promise((resolve) => setTimeout(resolve, 40));

			expect(timeoutFired).toBe(false);
			expect(animationFrameFired).toBe(false);
		});
	});

	describe('page.close()', () => {
		it('Rejects an in-flight Response body read with AbortError.', async () => {
			const browser = new Browser();
			const page = browser.newPage();
			const response = new page.mainFrame.window.Response(createHangingStream());
			const readPromise = expectAbortError(response.text());

			await page.close();
			await readPromise;
		});

		it('Keeps fully buffered Response bodies readable after the page is closed.', async () => {
			const browser = new Browser();
			const page = browser.newPage();
			const response = new page.mainFrame.window.Response('buffered');

			await page.close();

			expect(await response.text()).toBe('buffered');
		});
	});

	describe('browser.close()', () => {
		it('Rejects an in-flight Request body read with AbortError.', async () => {
			const browser = new Browser();
			const page = browser.newPage();
			const request = new page.mainFrame.window.Request(TEST_URL, {
				method: 'POST',
				body: createHangingStream()
			});
			const readPromise = expectAbortError(request.text());

			await browser.close();
			await readPromise;
		});
	});

	describe('navigation', () => {
		it('Rejects an in-flight Response body read when navigation discards page state.', async () => {
			const browser = new Browser();
			const page = browser.newPage();
			const response = new page.mainFrame.window.Response(createHangingStream());
			const readPromise = expectAbortError(response.blob());

			await page.goto('about:blank');
			await readPromise;
			await browser.close();
		});

		it('Clears timers and requestAnimationFrame callbacks for the discarded page.', async () => {
			const browser = new Browser();
			const page = browser.newPage();
			const previousWindow = page.mainFrame.window;
			let timeoutFired = false;
			let animationFrameFired = false;

			previousWindow.setTimeout(() => {
				timeoutFired = true;
			}, 20);
			previousWindow.requestAnimationFrame(() => {
				animationFrameFired = true;
			});

			await page.goto('about:blank');
			await new Promise((resolve) => setTimeout(resolve, 40));

			expect(timeoutFired).toBe(false);
			expect(animationFrameFired).toBe(false);
			await browser.close();
		});
	});
});
