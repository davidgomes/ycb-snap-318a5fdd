export function sanitizeMessage(message: string): string {
  return message
    .replace(/\bhttps?:\/\/[^\s]+/gi, '[redacted]')
    .replace(/\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b/gi, '[redacted]')
    .replace(/\b(?:\d{1,3}\.){3}\d{1,3}\b/g, '[redacted]');
}
