/**
 * Streaming debug utilities for diagnosing cloud deployment issues
 */

export const STREAMING_DEBUG = import.meta.env.VITE_STREAMING_DEBUG === 'true';

export function debugStreamingPerformance(startTime: number, firstResponseTime?: number, endTime?: number) {
  if (!STREAMING_DEBUG) return;

  const now = Date.now();
  console.group('🌊 Streaming Performance Debug');
  
  if (firstResponseTime) {
    console.log(`⏱️ Time to first response: ${firstResponseTime - startTime}ms`);
  }
  
  if (endTime) {
    console.log(`⏱️ Total request time: ${endTime - startTime}ms`);
  } else {
    console.log(`⏱️ Current request time: ${now - startTime}ms`);
  }
  
  console.groupEnd();
}

export function debugStreamingConnection(url: string, headers: HeadersInit) {
  if (!STREAMING_DEBUG) return;

  console.group('🔗 Streaming Connection Debug');
  console.log(`📡 URL: ${url}`);
  console.log(`📋 Headers:`, headers);
  console.groupEnd();
}

export function debugStreamingChunk(chunk: string, lineCount: number) {
  if (!STREAMING_DEBUG) return;

  console.group('📦 Streaming Chunk Debug');
  console.log(`📏 Chunk size: ${chunk.length} bytes`);
  console.log(`📝 Line count: ${lineCount}`);
  console.log(`🔍 First 100 chars: ${chunk.substring(0, 100)}`);
  console.groupEnd();
}

export function debugStreamingLatency(messageType: string, timestamp: number) {
  if (!STREAMING_DEBUG) return;

  const now = Date.now();
  const latency = now - timestamp;
  
  if (latency > 1000) {
    console.warn(`⚠️ High latency detected for ${messageType}: ${latency}ms`);
  } else {
    console.log(`⚡ ${messageType} latency: ${latency}ms`);
  }
}

export function warnProxyBuffering(detectionTime: number) {
  console.group('⚠️ Streaming Issue Detected');
  console.warn(`Proxy buffering suspected - no streaming detected within ${detectionTime}ms`);
  console.log('💡 Possible solutions:');
  console.log('  • Check NGINX/proxy configuration');
  console.log('  • Verify CDN settings');
  console.log('  • Check cloud platform streaming support');
  console.log('  • See STREAMING_DEPLOYMENT.md for details');
  console.groupEnd();
}