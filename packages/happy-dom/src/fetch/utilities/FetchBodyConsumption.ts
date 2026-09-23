import * as PropertySymbol from '../../PropertySymbol.js';
import type DOMException from '../../exception/DOMException.js';
import DOMExceptionNameEnum from '../../exception/DOMExceptionNameEnum.js';
import type BrowserWindow from '../../window/BrowserWindow.js';

/**
 * Request or response whose body read can be cancelled during shutdown.
 */
export interface IAbortableBody {
	[PropertySymbol.aborted]: boolean;
	[PropertySymbol.error]: Error | null;
	[PropertySymbol.cancelBodyConsumption]?: (() => void) | null;
}

/**
 * Shared body-consumption helpers.
 *
 * Kept separate from FetchBodyUtility so multipart parsing can abort in-flight reads
 * without a circular import.
 */
export default class FetchBodyConsumption {
	/**
	 * Marks a body read as aborted and settles any pending stream read.
	 *
	 * @param requestOrResponse Request or response.
	 */
	public static markAborted(requestOrResponse: IAbortableBody): void {
		requestOrResponse[PropertySymbol.aborted] = true;
		const cancel = requestOrResponse[PropertySymbol.cancelBodyConsumption];
		if (cancel) {
			requestOrResponse[PropertySymbol.cancelBodyConsumption] = null;
			cancel();
		}
	}

	/**
	 * Reads a stream until it closes, or rejects with AbortError when shutdown cancels it.
	 *
	 * @param window Window.
	 * @param requestOrResponse Request or response.
	 * @param reader Stream reader.
	 * @param onChunk Chunk callback.
	 */
	public static async readStream(
		window: BrowserWindow,
		requestOrResponse: IAbortableBody,
		reader: { read(): Promise<{ done: boolean; value?: any }>; cancel(reason?: any): Promise<void> },
		onChunk: (chunk: any) => void
	): Promise<void> {
		let notifyAbort: (() => void) | null = null;
		const aborted = new Promise<void>((resolve) => {
			notifyAbort = resolve;
		});
		const abortError = (): DOMException =>
			new window.DOMException(
				'Failed to read response body: The stream was aborted.',
				DOMExceptionNameEnum.abortError
			);
		const cancel = (): void => {
			notifyAbort?.();
			reader.cancel(abortError()).catch(() => {});
		};

		requestOrResponse[PropertySymbol.cancelBodyConsumption] = cancel;

		const readNext = (): Promise<{ type: 'read'; done: boolean; value?: any } | { type: 'abort' }> =>
			Promise.race([
				reader.read().then(
					(result) => ({ type: 'read' as const, done: result.done, value: result.value }),
					(error) => {
						if (requestOrResponse[PropertySymbol.aborted]) {
							return { type: 'abort' as const };
						}
						return Promise.reject(error);
					}
				),
				aborted.then(() => ({ type: 'abort' as const }))
			]);

		try {
			if (requestOrResponse[PropertySymbol.aborted]) {
				throw abortError();
			}

			let next = await readNext();
			while (next.type === 'read' && !next.done) {
				if (requestOrResponse[PropertySymbol.error]) {
					throw requestOrResponse[PropertySymbol.error];
				}
				if (requestOrResponse[PropertySymbol.aborted]) {
					throw abortError();
				}
				onChunk(next.value);
				next = await readNext();
			}

			if (next.type === 'abort' || requestOrResponse[PropertySymbol.aborted]) {
				throw abortError();
			}
		} finally {
			if (requestOrResponse[PropertySymbol.cancelBodyConsumption] === cancel) {
				requestOrResponse[PropertySymbol.cancelBodyConsumption] = null;
			}
		}
	}
}
