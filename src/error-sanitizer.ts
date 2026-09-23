const REDACTED = '[redacted]';

const URL_PATTERN = /\bhttps?:\/\/[^\s<>"'`]*[^\s<>"'`.,;:!?)\]}]/gi;

// The lookbehind keeps matching linear on long runs without an `@`.
const EMAIL_PATTERN = /(?<![\w.%+-])[\w.%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}/g;

const OCTET = '(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)';
const IPV4_PATTERN = new RegExp(`\\b${OCTET}(?:\\.${OCTET}){3}\\b`, 'g');

export function sanitizeMessage(message: string): string {
  if (typeof message !== 'string') {
    return message;
  }

  return message
    .replace(URL_PATTERN, REDACTED)
    .replace(EMAIL_PATTERN, REDACTED)
    .replace(IPV4_PATTERN, REDACTED);
}
