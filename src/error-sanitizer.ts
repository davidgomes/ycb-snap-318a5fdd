const TRAILING_URL_PUNCTUATION = /[.,;:!?]+$/;

/**
 * Replaces HTTP(S) URLs, email addresses, and IPv4 addresses with `[redacted]`.
 */
export function sanitizeMessage(message: string): string {
  if (typeof message !== 'string') {
    return message;
  }

  const withoutUrls = message.replace(
    /https?:\/\/[^\s<>"'()]+/gi,
    match => {
      const trimmed = match.replace(TRAILING_URL_PUNCTUATION, '');
      if (!/^https?:\/\/.+/i.test(trimmed)) {
        return match;
      }
      return '[redacted]' + match.slice(trimmed.length);
    }
  );

  const withoutEmails = withoutUrls.replace(
    /\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b/g,
    '[redacted]'
  );

  return withoutEmails.replace(
    /\b(?:(?:25[0-5]|2[0-4]\d|[01]?\d\d?)\.){3}(?:25[0-5]|2[0-4]\d|[01]?\d\d?)\b/g,
    '[redacted]'
  );
}
