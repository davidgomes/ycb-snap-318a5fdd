import DOMExceptionNameEnum from '../../exception/DOMExceptionNameEnum.js';
import type BrowserWindow from '../../window/BrowserWindow.js';

const activeBodyReaders = new WeakMap<object, ReadableStreamDefaultReader<unknown>>();

/**
 * Tracks in-flight request and response body readers so shutdown can cancel them.
 */
export default class BodyReadRegistry {
	/**
	 * Tracks the reader used to consume a request or response body.
	 *
	 * @param requestOrResponse Request or response.
	 * @param reader Reader.
	 */
	public static track(
		requestOrResponse: object,
		reader: ReadableStreamDefaultReader<unknown>
	): void {
		activeBodyReaders.set(requestOrResponse, reader);
	}

	/**
	 * Stops tracking a body reader.
	 *
	 * @param requestOrResponse Request or response.
	 */
	public static untrack(requestOrResponse: object): void {
		activeBodyReaders.delete(requestOrResponse);
	}

	/**
	 * Cancels an in-flight body read.
	 *
	 * @param window Window.
	 * @param requestOrResponse Request or response.
	 */
	public static abort(window: BrowserWindow, requestOrResponse: object): void {
		const reader = activeBodyReaders.get(requestOrResponse);
		if (!reader) {
			return;
		}
		reader
			.cancel(
				new window.DOMException(
					'Failed to read response body: The stream was aborted.',
					DOMExceptionNameEnum.abortError
				)
			)
			.catch(() => {});
	}
}
