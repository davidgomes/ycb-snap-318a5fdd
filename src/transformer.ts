import {
  isBigint,
  isDate,
  isInfinite,
  isMap,
  isNaNValue,
  isRegExp,
  isSet,
  isUndefined,
  isSymbol,
  isArray,
  isError,
  isTypedArray,
  TypedArrayConstructor,
  isURL,
} from './is.js';
import { findArr } from './util.js';
import SuperJSON from './index.js';
import { sanitizeMessage } from './error-sanitizer.js';
import { processStackFrames, processStackString } from './error-stack.js';
import type { NormalizedErrorStackOptions } from './error-options.js';

export type PrimitiveTypeAnnotation = 'number' | 'undefined' | 'bigint';

type LeafTypeAnnotation = PrimitiveTypeAnnotation | 'regexp' | 'Date' | 'URL';

type TypedArrayAnnotation = ['typed-array', string];
type ClassTypeAnnotation = ['class', string];
type SymbolTypeAnnotation = ['symbol', string];
type CustomTypeAnnotation = ['custom', string];

type SimpleTypeAnnotation =
  | LeafTypeAnnotation
  | 'map'
  | 'set'
  | 'Error'
  | 'Error/stack'
  | 'Error/frames';

const errorCauseDepths = new WeakMap<object, number>();

export function getErrorCauseDepth(superJson: object): number {
  return errorCauseDepths.get(superJson) ?? 0;
}

export function adjustErrorCauseDepth(superJson: object, delta: number): void {
  errorCauseDepths.set(superJson, getErrorCauseDepth(superJson) + delta);
}

function matchesClassFilter(
  error: { name?: string },
  classFilter: string[] | undefined
): boolean {
  if (!classFilter || classFilter.length === 0) {
    return true;
  }
  return classFilter.includes(error.name ?? '');
}

function stackPropAllowed(superJson: SuperJSON, prop: string): boolean {
  return superJson.allowedErrorProps.includes(prop);
}

function maybeSanitizeMessage(
  error: Error,
  options: NormalizedErrorStackOptions
): string {
  if (!options.sanitizeMessage || typeof error.message !== 'string') {
    return error.message;
  }
  if (!matchesClassFilter(error, options.classFilter)) {
    return error.message;
  }
  return sanitizeMessage(error.message);
}

function shouldIncludeCause(error: Error, superJson: SuperJSON): boolean {
  const options = superJson.errorStack;
  if (!options) {
    return 'cause' in error;
  }

  const cause = (error as { cause?: unknown }).cause;
  if (!isError(cause)) {
    return false;
  }
  if (options.includeCauses === 'direct') {
    return getErrorCauseDepth(superJson) === 0;
  }
  if (options.includeCauses === 'deep') {
    return getErrorCauseDepth(superJson) < options.maxCauseDepth;
  }
  return false;
}

function serializeError(
  error: Error,
  superJson: SuperJSON,
  variant: 'default' | 'string' | 'frames'
): Record<string, unknown> {
  const options = superJson.errorStack;

  if (!options) {
    const baseError: Record<string, unknown> = {
      name: error.name,
      message: error.message,
    };

    if ('cause' in error) {
      baseError.cause = (error as { cause?: unknown }).cause;
    }

    superJson.allowedErrorProps.forEach(prop => {
      baseError[prop] = (error as any)[prop];
    });

    return baseError;
  }

  const baseError: Record<string, unknown> = {
    name: error.name,
    message: maybeSanitizeMessage(error, options),
  };

  if (shouldIncludeCause(error, superJson)) {
    baseError.cause = (error as { cause?: unknown }).cause;
  }

  superJson.allowedErrorProps.forEach(prop => {
    if (
      prop === 'stack' ||
      prop === 'stackFrames' ||
      prop === 'cause' ||
      prop === 'message' ||
      prop === 'name'
    ) {
      return;
    }
    baseError[prop] = (error as any)[prop];
  });

  if (typeof AggregateError !== 'undefined' && error instanceof AggregateError) {
    baseError.errors = error.errors;
  }

  const allowStack = stackPropAllowed(superJson, 'stack');
  const allowFrames = stackPropAllowed(superJson, 'stackFrames');

  if (
    variant === 'string' &&
    allowStack &&
    typeof error.stack === 'string'
  ) {
    baseError.stack = processStackString(error.stack, options);
  } else if (
    variant === 'frames' &&
    allowFrames &&
    typeof error.stack === 'string'
  ) {
    baseError.stackFrames = processStackFrames(error.stack, options);
  } else if (variant === 'default' && options.mode !== 'off') {
    if (allowStack && error.stack !== undefined) {
      baseError.stack = error.stack;
    }
    const rawFrames = (error as { stackFrames?: unknown }).stackFrames;
    if (allowFrames && rawFrames !== undefined) {
      baseError.stackFrames = rawFrames;
    }
  }

  return baseError;
}

function untransformError(v: any, superJson: SuperJSON): Error {
  const error =
    v?.name === 'AggregateError' && Array.isArray(v.errors)
      ? new AggregateError(v.errors, v.message, { cause: v.cause })
      : new Error(v.message, { cause: v.cause });

  error.name = v.name;
  error.stack = v.stack;

  if (v.stackFrames !== undefined) {
    (error as { stackFrames?: unknown }).stackFrames = v.stackFrames;
  }

  superJson.allowedErrorProps.forEach(prop => {
    (error as any)[prop] = v[prop];
  });

  return error;
}

function errorRuleIsApplicable(
  mode: 'string' | 'frames'
): (value: unknown, superJson: SuperJSON) => value is Error {
  return (value, superJson): value is Error => {
    if (!isError(value)) {
      return false;
    }
    const options = superJson.errorStack;
    if (!options || options.mode !== mode) {
      return false;
    }
    return matchesClassFilter(value, options.classFilter);
  };
}

type CompositeTypeAnnotation =
  | TypedArrayAnnotation
  | ClassTypeAnnotation
  | SymbolTypeAnnotation
  | CustomTypeAnnotation;

export type TypeAnnotation = SimpleTypeAnnotation | CompositeTypeAnnotation;

function simpleTransformation<I, O, A extends SimpleTypeAnnotation>(
  isApplicable: (v: any, superJson: SuperJSON) => v is I,
  annotation: A,
  transform: (v: I, superJson: SuperJSON) => O,
  untransform: (v: O, superJson: SuperJSON) => I
) {
  return {
    isApplicable,
    annotation,
    transform,
    untransform,
  };
}

const simpleRules = [
  simpleTransformation(
    isUndefined,
    'undefined',
    () => null,
    () => undefined
  ),
  simpleTransformation(
    isBigint,
    'bigint',
    v => v.toString(),
    v => {
      if (typeof BigInt !== 'undefined') {
        return BigInt(v);
      }

      console.error('Please add a BigInt polyfill.');

      return v as any;
    }
  ),
  simpleTransformation(
    isDate,
    'Date',
    v => v.toISOString(),
    v => new Date(v)
  ),

  simpleTransformation(
    errorRuleIsApplicable('string'),
    'Error/stack',
    (v, superJson) => serializeError(v, superJson, 'string'),
    untransformError
  ),
  simpleTransformation(
    errorRuleIsApplicable('frames'),
    'Error/frames',
    (v, superJson) => serializeError(v, superJson, 'frames'),
    untransformError
  ),
  simpleTransformation(
    isError,
    'Error',
    (v, superJson) => serializeError(v, superJson, 'default'),
    untransformError
  ),

  simpleTransformation(
    isRegExp,
    'regexp',
    v => '' + v,
    regex => {
      const body = regex.slice(1, regex.lastIndexOf('/'));
      const flags = regex.slice(regex.lastIndexOf('/') + 1);
      return new RegExp(body, flags);
    }
  ),

  simpleTransformation(
    isSet,
    'set',
    // (sets only exist in es6+)
    // eslint-disable-next-line es5/no-es6-methods
    v => [...v.values()],
    v => new Set(v)
  ),
  simpleTransformation(
    isMap,
    'map',
    v => [...v.entries()],
    v => new Map(v)
  ),

  simpleTransformation<number, 'NaN' | 'Infinity' | '-Infinity', 'number'>(
    (v): v is number => isNaNValue(v) || isInfinite(v),
    'number',
    v => {
      if (isNaNValue(v)) {
        return 'NaN';
      }

      if (v > 0) {
        return 'Infinity';
      } else {
        return '-Infinity';
      }
    },
    Number
  ),

  simpleTransformation<number, '-0', 'number'>(
    (v): v is number => v === 0 && 1 / v === -Infinity,
    'number',
    () => {
      return '-0';
    },
    Number
  ),

  simpleTransformation(
    isURL,
    'URL',
    v => v.toString(),
    v => new URL(v)
  ),
];

function compositeTransformation<I, O, A extends CompositeTypeAnnotation>(
  isApplicable: (v: any, superJson: SuperJSON) => v is I,
  annotation: (v: I, superJson: SuperJSON) => A,
  transform: (v: I, superJson: SuperJSON) => O,
  untransform: (v: O, a: A, superJson: SuperJSON) => I
) {
  return {
    isApplicable,
    annotation,
    transform,
    untransform,
  };
}

const symbolRule = compositeTransformation(
  (s, superJson): s is Symbol => {
    if (isSymbol(s)) {
      const isRegistered = !!superJson.symbolRegistry.getIdentifier(s);
      return isRegistered;
    }
    return false;
  },
  (s, superJson) => {
    const identifier = superJson.symbolRegistry.getIdentifier(s);
    return ['symbol', identifier!];
  },
  v => v.description,
  (_, a, superJson) => {
    const value = superJson.symbolRegistry.getValue(a[1]);
    if (!value) {
      throw new Error('Trying to deserialize unknown symbol');
    }
    return value;
  }
);

const constructorToName = [
  Int8Array,
  Uint8Array,
  Int16Array,
  Uint16Array,
  Int32Array,
  Uint32Array,
  Float32Array,
  Float64Array,
  Uint8ClampedArray,
].reduce<Record<string, TypedArrayConstructor>>((obj, ctor) => {
  obj[ctor.name] = ctor;
  return obj;
}, {});

const typedArrayRule = compositeTransformation(
  isTypedArray,
  v => ['typed-array', v.constructor.name],
  v => [...v],
  (v, a) => {
    const ctor = constructorToName[a[1]];

    if (!ctor) {
      throw new Error('Trying to deserialize unknown typed array');
    }

    return new ctor(v);
  }
);

export function isInstanceOfRegisteredClass(
  potentialClass: any,
  superJson: SuperJSON
): potentialClass is any {
  if (potentialClass?.constructor) {
    const isRegistered = !!superJson.classRegistry.getIdentifier(
      potentialClass.constructor
    );
    return isRegistered;
  }
  return false;
}

const classRule = compositeTransformation(
  isInstanceOfRegisteredClass,
  (clazz, superJson) => {
    const identifier = superJson.classRegistry.getIdentifier(clazz.constructor);
    return ['class', identifier!];
  },
  (clazz, superJson) => {
    const allowedProps = superJson.classRegistry.getAllowedProps(
      clazz.constructor
    );
    if (!allowedProps) {
      return { ...clazz };
    }

    const result: any = {};
    allowedProps.forEach(prop => {
      result[prop] = clazz[prop];
    });
    return result;
  },
  (v, a, superJson) => {
    const clazz = superJson.classRegistry.getValue(a[1]);

    if (!clazz) {
      throw new Error(
        `Trying to deserialize unknown class '${a[1]}' - check https://github.com/blitz-js/superjson/issues/116#issuecomment-773996564`
      );
    }

    return Object.assign(Object.create(clazz.prototype), v);
  }
);

const customRule = compositeTransformation(
  (value, superJson): value is any => {
    return !!superJson.customTransformerRegistry.findApplicable(value);
  },
  (value, superJson) => {
    const transformer = superJson.customTransformerRegistry.findApplicable(
      value
    )!;
    return ['custom', transformer.name];
  },
  (value, superJson) => {
    const transformer = superJson.customTransformerRegistry.findApplicable(
      value
    )!;
    return transformer.serialize(value);
  },
  (v, a, superJson) => {
    const transformer = superJson.customTransformerRegistry.findByName(a[1]);
    if (!transformer) {
      throw new Error('Trying to deserialize unknown custom value');
    }
    return transformer.deserialize(v);
  }
);

const compositeRules = [classRule, symbolRule, customRule, typedArrayRule];

export const transformValue = (
  value: any,
  superJson: SuperJSON
): { value: any; type: TypeAnnotation } | undefined => {
  const applicableCompositeRule = findArr(compositeRules, rule =>
    rule.isApplicable(value, superJson)
  );
  if (applicableCompositeRule) {
    return {
      value: applicableCompositeRule.transform(value as never, superJson),
      type: applicableCompositeRule.annotation(value, superJson),
    };
  }

  const applicableSimpleRule = findArr(simpleRules, rule =>
    rule.isApplicable(value, superJson)
  );

  if (applicableSimpleRule) {
    return {
      value: applicableSimpleRule.transform(value as never, superJson),
      type: applicableSimpleRule.annotation,
    };
  }

  return undefined;
};

const simpleRulesByAnnotation: Record<string, typeof simpleRules[0]> = {};
simpleRules.forEach(rule => {
  simpleRulesByAnnotation[rule.annotation] = rule;
});

export const untransformValue = (
  json: any,
  type: TypeAnnotation,
  superJson: SuperJSON
) => {
  if (isArray(type)) {
    switch (type[0]) {
      case 'symbol':
        return symbolRule.untransform(json, type, superJson);
      case 'class':
        return classRule.untransform(json, type, superJson);
      case 'custom':
        return customRule.untransform(json, type, superJson);
      case 'typed-array':
        return typedArrayRule.untransform(json, type, superJson);
      default:
        throw new Error('Unknown transformation: ' + type);
    }
  } else {
    const transformation = simpleRulesByAnnotation[type];
    if (!transformation) {
      throw new Error('Unknown transformation: ' + type);
    }

    return transformation.untransform(json as never, superJson);
  }
};
