import { ReadableStream } from 'stream/web';
import { describe, it, expect } from 'vitest';
import Browser from '../../src/browser/Browser.js';
import DOMExceptionNameEnum from '../../src/exception/DOMExceptionNameEnum.js';
import type BrowserWindow from '../../src/window/BrowserWindow.js';
import Window from '../../src/window/Window.js';

/**
 * Stream that never produces data, so a body read stays pending until shutdown.
 */
function hangingStream(): ReadableStream {
	return new ReadableStream({
		start() {
			// Leave the stream open.
		}
	});
}

/**
 * Asserts that a body read rejected with AbortError.
 *
 * @param pending Read promise.
 * @param window Window that owns the DOMException realm.
 */
async function expectAbortError(pending: Promise<unknown>, window: BrowserWindow): Promise<void> {
	const error = await pending.then(
		() => {
			throw new Error('Expected the body read to reject.');
		},
		(reason: unknown) => reason
	);

	expect(error).toBeInstanceOf(window.DOMException);
	expect((<Error>error).name).toBe(DOMExceptionNameEnum.abortError);
}

describe('Body consumption shutdown', () => {
	it('Does not change a successful response read.', async () => {
		const window = new Window();
		const response = new window.Response('Hello World');

		expect(await response.text()).toBe('Hello World');

		await window.happyDOM.close();
	});

	it('Keeps a fully buffered response readable after happyDOM.close().', async () => {
		const window = new Window();
		const textResponse = new window.Response('buffered-text');
		const jsonResponse = new window.Response('{"ok":true}');
		const binaryResponse = new window.Response('buffered-binary');
		const formData = new window.FormData();
		formData.append('name', 'Ada');
		const multipartResponse = new window.Response(formData);
		const urlEncodedResponse = new window.Response('city=Paris', {
			headers: { 'Content-Type': 'application/x-www-form-urlencoded' }
		});

		await window.happyDOM.close();

		expect(await textResponse.text()).toBe('buffered-text');
		expect(await jsonResponse.json()).toEqual({ ok: true });
		expect(Buffer.from(await binaryResponse.arrayBuffer()).toString()).toBe('buffered-binary');
		expect((await multipartResponse.formData()).get('name')).toBe('Ada');
		expect((await urlEncodedResponse.formData()).get('city')).toBe('Paris');
	});

	it('Rejects an interrupted response read when happyDOM.close() is called.', async () => {
		const window = new Window();
		const response = new window.Response(hangingStream());
		const pending = response.text();

		const assertion = expectAbortError(pending, window);
		await window.happyDOM.close();
		await assertion;
	});

	it('Rejects an interrupted request read when happyDOM.close() is called.', async () => {
		const window = new Window();
		const request = new window.Request('https://example.com/', {
			method: 'POST',
			body: hangingStream()
		});
		const pending = request.arrayBuffer();

		const assertion = expectAbortError(pending, window);
		await window.happyDOM.close();
		await assertion;
	});

	it('Rejects an interrupted multipart formData() read when happyDOM.close() is called.', async () => {
		const window = new Window();
		const response = new window.Response(hangingStream(), {
			headers: { 'Content-Type': 'multipart/form-data; boundary=----HappyDOMBoundary' }
		});
		const request = new window.Request('https://example.com/', {
			method: 'POST',
			headers: { 'Content-Type': 'multipart/form-data; boundary=----HappyDOMBoundary' },
			body: hangingStream()
		});
		const responsePending = response.formData();
		const requestPending = request.formData();

		const responseAssertion = expectAbortError(responsePending, window);
		const requestAssertion = expectAbortError(requestPending, window);
		await window.happyDOM.close();
		await responseAssertion;
		await requestAssertion;
	});

	it('Rejects an interrupted response read when page.close() is called.', async () => {
		const browser = new Browser();
		const page = browser.newPage();
		const window = page.mainFrame.window;
		const iframe = window.document.createElement('iframe');
		iframe.srcdoc = '<html><body>child</body></html>';
		window.document.body.appendChild(iframe);

		const response = new window.Response(hangingStream());
		const pending = response.text();

		const assertion = expectAbortError(pending, window);
		await page.close();
		await assertion;
		await browser.close();
	});

	it('Rejects an interrupted response read when browser.close() is called.', async () => {
		const browser = new Browser();
		const page = browser.newPage();
		const window = page.mainFrame.window;
		const response = new window.Response(hangingStream());
		const pending = response.blob();

		const assertion = expectAbortError(pending, window);
		await browser.close();
		await assertion;
	});

	it('Rejects an interrupted response read when navigation discards the page.', async () => {
		const browser = new Browser();
		const page = browser.newPage();
		const window = page.mainFrame.window;
		const iframe = window.document.createElement('iframe');
		iframe.srcdoc = '<html><body>child</body></html>';
		window.document.body.appendChild(iframe);

		const response = new window.Response(hangingStream());
		const pending = response.text();
		const assertion = expectAbortError(pending, window);

		window.location.href = 'about:srcdoc';
		await assertion;
		await browser.close();
	});

	it('Clears timers and requestAnimationFrame callbacks for a discarded page.', async () => {
		const browser = new Browser();
		const page = browser.newPage();
		const window = page.mainFrame.window;
		const iframe = window.document.createElement('iframe');
		iframe.srcdoc = '<html><body>child</body></html>';
		window.document.body.appendChild(iframe);

		let timeoutFired = false;
		let intervalFired = false;
		let animationFrameFired = false;

		window.setTimeout(() => {
			timeoutFired = true;
		}, 0);
		window.setInterval(() => {
			intervalFired = true;
		}, 0);
		window.requestAnimationFrame(() => {
			animationFrameFired = true;
		});

		window.location.href = 'about:srcdoc';

		await new Promise((resolve) => setTimeout(resolve, 30));

		expect(timeoutFired).toBe(false);
		expect(intervalFired).toBe(false);
		expect(animationFrameFired).toBe(false);

		await browser.close();
	});
});
