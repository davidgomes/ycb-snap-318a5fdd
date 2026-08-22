const URL_RE = /https?:\/\/[^\s]+/gi;
const EMAIL_RE = /[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[A-Za-z]{2,}/g;
const IPV4_RE = /\b(?:\d{1,3}\.){3}\d{1,3}\b/g;

const REDACTED = '[redacted]';

/**
 * Replace HTTP/HTTPS URLs, email addresses, and IPv4 addresses with `[redacted]`.
 */
export function sanitizeMessage(message: string): string {
  if (typeof message !== 'string') {
    return message;
  }

  return message
    .replace(URL_RE, REDACTED)
    .replace(EMAIL_RE, REDACTED)
    .replace(IPV4_RE, REDACTED);
}
