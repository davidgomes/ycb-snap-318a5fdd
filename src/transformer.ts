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
import type { NormalizedErrorStackOptions } from './error-options.js';
import { sanitizeMessage } from './error-sanitizer.js';
import {
  processStackFrames,
  processStackString,
} from './error-stack.js';

export type PrimitiveTypeAnnotation = 'number' | 'undefined' | 'bigint';

type LeafTypeAnnotation = PrimitiveTypeAnnotation | 'regexp' | 'Date' | 'URL';

type TypedArrayAnnotation = ['typed-array', string];
type ClassTypeAnnotation = ['class', string];
type SymbolTypeAnnotation = ['symbol', string];
type CustomTypeAnnotation = ['custom', string];

export type ErrorTypeAnnotation = 'Error' | 'Error/stack' | 'Error/frames';

type SimpleTypeAnnotation = LeafTypeAnnotation | 'map' | 'set' | ErrorTypeAnnotation;

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

function matchesClassFilter(
  name: string,
  options: NormalizedErrorStackOptions
): boolean {
  const filter = options.classFilter;
  if (!filter || filter.length === 0) {
    return true;
  }
  return filter.includes(name);
}

function resolveErrorAnnotation(
  value: Error,
  superJson: SuperJSON
): ErrorTypeAnnotation {
  const options = superJson.errorStackOptions;
  if (!options || options.mode === 'off') {
    return 'Error';
  }
  if (!matchesClassFilter(value.name, options)) {
    return 'Error';
  }
  if (options.mode === 'frames') {
    return 'Error/frames';
  }
  if (options.mode === 'string') {
    return 'Error/stack';
  }
  return 'Error';
}

function allowsErrorProp(superJson: SuperJSON, prop: string): boolean {
  return superJson.allowedErrorProps.indexOf(prop) !== -1;
}

function serializeLegacyError(value: Error, superJson: SuperJSON) {
  const baseError: any = {
    name: value.name,
    message: value.message,
  };

  if ('cause' in value) {
    baseError.cause = (value as any).cause;
  }

  superJson.allowedErrorProps.forEach(prop => {
    baseError[prop] = (value as any)[prop];
  });

  return baseError;
}

function attachCause(
  baseError: any,
  value: Error,
  superJson: SuperJSON,
  options: NormalizedErrorStackOptions
) {
  if (options.includeCauses === 'none' || !('cause' in value)) {
    return;
  }

  const cause = (value as any).cause;
  if (!isError(cause)) {
    return;
  }

  const depth = superJson.getErrorCauseDepth(value);
  if (options.includeCauses === 'direct') {
    if (depth !== 0) {
      return;
    }
  } else if (depth >= options.maxCauseDepth) {
    return;
  }

  superJson.setErrorCauseDepth(cause, depth + 1);
  baseError.cause = cause;
}

function serializeConfiguredError(
  value: Error,
  superJson: SuperJSON,
  options: NormalizedErrorStackOptions
) {
  const kind = resolveErrorAnnotation(value, superJson);
  const classMatches = matchesClassFilter(value.name, options);
  const message =
    options.sanitizeMessage && classMatches
      ? sanitizeMessage(value.message)
      : value.message;

  const baseError: any = {
    name: value.name,
    message,
  };

  attachCause(baseError, value, superJson, options);

  if (typeof AggregateError !== 'undefined' && value instanceof AggregateError) {
    baseError.errors = (value as AggregateError).errors;
  }

  const suppressStackProps =
    options.mode === 'off' || kind === 'Error/stack' || kind === 'Error/frames';

  superJson.allowedErrorProps.forEach(prop => {
    if (prop === 'name' || prop === 'message' || prop === 'cause') {
      return;
    }
    if (prop === 'errors' && baseError.errors !== undefined) {
      return;
    }
    if (suppressStackProps && (prop === 'stack' || prop === 'stackFrames')) {
      return;
    }
    baseError[prop] = (value as any)[prop];
  });

  baseError.name = value.name;
  baseError.message = message;

  if (
    kind === 'Error/stack' &&
    allowsErrorProp(superJson, 'stack') &&
    typeof value.stack === 'string'
  ) {
    baseError.stack = processStackString(value.stack, options);
  }

  if (
    kind === 'Error/frames' &&
    allowsErrorProp(superJson, 'stackFrames') &&
    typeof value.stack === 'string'
  ) {
    baseError.stackFrames = processStackFrames(value.stack, options);
  }

  return baseError;
}

function serializeError(value: Error, superJson: SuperJSON) {
  const options = superJson.errorStackOptions;
  if (!options) {
    return serializeLegacyError(value, superJson);
  }
  return serializeConfiguredError(value, superJson, options);
}

function reviveError(value: any): Error {
  const causeOptions = { cause: value?.cause };
  if (
    typeof AggregateError !== 'undefined' &&
    value?.name === 'AggregateError' &&
    Array.isArray(value.errors)
  ) {
    return new AggregateError(value.errors, value.message, causeOptions);
  }

  const error = new Error(value.message, causeOptions);
  if (Array.isArray(value?.errors)) {
    (error as any).errors = value.errors;
  }
  return error;
}

function stackFromFrames(frames: any[]): string {
  return frames
    .map(frame => (frame && typeof frame.raw === 'string' ? frame.raw : ''))
    .join('\n');
}

function deserializeError(
  value: any,
  superJson: SuperJSON,
  annotation: ErrorTypeAnnotation
) {
  const error = reviveError(value);
  error.name = value.name;

  if (annotation === 'Error/frames' && Array.isArray(value.stackFrames)) {
    error.stack = stackFromFrames(value.stackFrames);
  } else {
    error.stack = value.stack;
  }

  superJson.allowedErrorProps.forEach(prop => {
    (error as any)[prop] = value[prop];
  });

  if (annotation === 'Error/frames' && Array.isArray(value.stackFrames)) {
    error.stack = stackFromFrames(value.stackFrames);
  }

  return error;
}

function errorRule(annotation: ErrorTypeAnnotation) {
  return simpleTransformation(
    (value, superJson): value is Error =>
      isError(value) && resolveErrorAnnotation(value, superJson) === annotation,
    annotation,
    (value, superJson) => serializeError(value, superJson),
    (value, superJson) => deserializeError(value, superJson, annotation)
  );
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

  errorRule('Error/stack'),
  errorRule('Error/frames'),
  errorRule('Error'),

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
