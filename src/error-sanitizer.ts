const TRAILING_URL_PUNCTUATION = /[),.;:!?]+$/;

/**
 * Replaces HTTP(S) URLs, email addresses, and IPv4 addresses with `[redacted]`.
 */
export function sanitizeMessage(message: string): string {
  if (typeof message !== 'string' || message.length === 0) {
    return message;
  }

  const urlRe = /\bhttps?:\/\/[^\s<>"'`]+/gi;
  const emailRe = /\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b/g;
  const ipv4Re =
    /\b(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\b/g;

  return message
    .replace(urlRe, match => {
      const trailing = match.match(TRAILING_URL_PUNCTUATION);
      if (!trailing) {
        return '[redacted]';
      }
      return '[redacted]' + trailing[0];
    })
    .replace(emailRe, '[redacted]')
    .replace(ipv4Re, '[redacted]');
}
