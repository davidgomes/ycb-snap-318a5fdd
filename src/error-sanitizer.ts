const URL_RE = /https?:\/\/[^\s]+/gi;
const EMAIL_RE = /[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}/g;
const IPV4_RE = /\b(?:\d{1,3}\.){3}\d{1,3}\b/g;

export function sanitizeMessage(message: string): string {
  if (typeof message !== 'string') return message;
  return message
    .replace(URL_RE, '[redacted]')
    .replace(EMAIL_RE, '[redacted]')
    .replace(IPV4_RE, '[redacted]');
}
