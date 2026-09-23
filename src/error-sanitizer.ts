const TRAILING_URL_PUNCTUATION = /[.,;:!?]+$/;

function replaceUrl(match: string): string {
  const trimmed = match.replace(TRAILING_URL_PUNCTUATION, '');
  return '[redacted]' + match.slice(trimmed.length);
}

export function sanitizeMessage(message: string): string {
  if (typeof message !== 'string') {
    return message;
  }

  const withoutUrls = message.replace(/https?:\/\/[^\s<>"'()]+/gi, replaceUrl);
  const withoutEmails = withoutUrls.replace(
    /[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}/gi,
    '[redacted]'
  );
  return withoutEmails.replace(
    /(?<!\d\.)\b(?:(?:25[0-5]|2[0-4]\d|[01]?\d\d?)\.){3}(?:25[0-5]|2[0-4]\d|[01]?\d\d?)(?!\.\d)\b/g,
    '[redacted]'
  );
}
