import { StackFrame } from './error-stack.js';

export interface SerializedError {
  name: string;
  message: string;
  stack?: string;
  stackFrames?: StackFrame[];
  cause?: unknown;
  errors?: unknown;
  [key: string]: unknown;
}

export type Processor = (serialized: SerializedError) => SerializedError;

export class ErrorClassRegistry {
  private processors = new Map<string, Processor>();

  register(name: string, fn: Processor): void {
    this.processors.set(name, fn);
  }

  has(name: string): boolean {
    return this.processors.has(name);
  }

  getProcessor(name: string): Processor | undefined {
    return this.processors.get(name);
  }
}
