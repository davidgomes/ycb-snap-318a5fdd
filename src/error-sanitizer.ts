const REDACTED = '[redacted]';

const URL_PATTERN = /\bhttps?:\/\/[^\s<>"'`]+/gi;
const URL_TRAILING_PUNCTUATION = /[.,;:!?)\]}]+$/;
const EMAIL_PATTERN = /[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}/g;
const IPV4_OCTET = '(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)';
const IPV4_PATTERN = new RegExp(
  `(?<!\\d|\\d\\.)${IPV4_OCTET}(?:\\.${IPV4_OCTET}){3}(?!\\d|\\.\\d)`,
  'g'
);

export function sanitizeMessage(message: string): string {
  if (typeof message !== 'string') {
    return message;
  }
  return message
    .replace(URL_PATTERN, url => {
      const trailing = URL_TRAILING_PUNCTUATION.exec(url)?.[0] ?? '';
      return REDACTED + trailing;
    })
    .replace(EMAIL_PATTERN, REDACTED)
    .replace(IPV4_PATTERN, REDACTED);
}
