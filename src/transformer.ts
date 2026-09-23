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
import { initialCauseDepth, matchesClassFilter } from './error-options.js';
import { processStackFrames, processStackString } from './error-stack.js';
import { sanitizeMessage } from './error-sanitizer.js';
import { SerializedError } from './error-class-registry.js';

export type PrimitiveTypeAnnotation = 'number' | 'undefined' | 'bigint';

type LeafTypeAnnotation = PrimitiveTypeAnnotation | 'regexp' | 'Date' | 'URL';

type TypedArrayAnnotation = ['typed-array', string];
type ClassTypeAnnotation = ['class', string];
type SymbolTypeAnnotation = ['symbol', string];
type CustomTypeAnnotation = ['custom', string];

type ErrorTypeAnnotation = 'Error' | 'Error/stack' | 'Error/frames';

type SimpleTypeAnnotation =
  | LeafTypeAnnotation
  | 'map'
  | 'set'
  | ErrorTypeAnnotation;

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

export interface ErrorCauseContext {
  remainingCauseDepth: number;
  sanitize: boolean;
}

function errorAnnotationFor(
  error: Error,
  superJson: SuperJSON
): ErrorTypeAnnotation {
  const options = superJson.errorStackOptions;
  if (
    !options ||
    options.mode === 'off' ||
    !matchesClassFilter(options, error.name)
  ) {
    return 'Error';
  }
  return options.mode === 'string' ? 'Error/stack' : 'Error/frames';
}

function isAggregateError(error: Error): boolean {
  return (
    isArray((error as any).errors) &&
    (error.name === 'AggregateError' ||
      (typeof AggregateError === 'function' &&
        error instanceof AggregateError))
  );
}

function applyErrorProcessor(
  error: Error,
  serialized: SerializedError,
  superJson: SuperJSON
): SerializedError {
  const registry = superJson.errorClassRegistry;
  const processor =
    registry.getProcessor(error.name) ??
    registry.getProcessor(error.constructor?.name);
  if (!processor) {
    return serialized;
  }
  const replacement = processor(serialized);
  return typeof replacement === 'object' && replacement !== null
    ? replacement
    : serialized;
}

function transformError(
  v: Error,
  superJson: SuperJSON,
  annotation: ErrorTypeAnnotation
): SerializedError {
  const options = superJson.errorStackOptions;
  const baseError: SerializedError = {
    name: v.name,
    message: v.message,
  };

  if (!options) {
    if ('cause' in v) {
      baseError.cause = v.cause;
    }

    superJson.allowedErrorProps.forEach(prop => {
      baseError[prop] = (v as any)[prop];
    });

    return applyErrorProcessor(v, baseError, superJson);
  }

  const allowedProps = superJson.allowedErrorProps;
  allowedProps.forEach(prop => {
    // stack data and causes are governed by the errorStack options
    if (prop !== 'stack' && prop !== 'stackFrames' && prop !== 'cause') {
      baseError[prop] = (v as any)[prop];
    }
  });

  if (typeof v.stack === 'string') {
    if (annotation === 'Error/stack' && allowedProps.includes('stack')) {
      baseError.stack = processStackString(v.stack, options);
    } else if (
      annotation === 'Error/frames' &&
      allowedProps.includes('stackFrames')
    ) {
      baseError.stackFrames = processStackFrames(v.stack, options);
    }
  }

  const context = superJson.errorCauseContexts.get(v);
  superJson.errorCauseContexts.delete(v);

  const sanitize =
    options.sanitizeMessage &&
    (!!context?.sanitize || matchesClassFilter(options, v.name));
  if (sanitize) {
    baseError.message = sanitizeMessage(baseError.message);
  }

  const remainingCauseDepth = context
    ? context.remainingCauseDepth
    : initialCauseDepth(options);
  if (remainingCauseDepth > 0 && isError(v.cause)) {
    baseError.cause = v.cause;
    superJson.errorCauseContexts.set(v.cause, {
      remainingCauseDepth: remainingCauseDepth - 1,
      sanitize,
    });
  }

  if (isAggregateError(v)) {
    baseError.errors = (v as any).errors;
  }

  return applyErrorProcessor(v, baseError, superJson);
}

function untransformError(
  v: any,
  superJson: SuperJSON,
  annotation: ErrorTypeAnnotation
): Error {
  let e: Error;
  if (annotation === 'Error' && !superJson.errorStackOptions) {
    e = new Error(v.message, { cause: v.cause });
  } else {
    const errorOptions = 'cause' in v ? { cause: v.cause } : undefined;
    e =
      v.name === 'AggregateError' &&
      isArray(v.errors) &&
      typeof AggregateError === 'function'
        ? new AggregateError(v.errors, v.message, errorOptions)
        : new Error(v.message, errorOptions);
  }
  e.name = v.name;
  e.stack = v.stack;

  if (annotation === 'Error/frames' && 'stackFrames' in v) {
    (e as any).stackFrames = v.stackFrames;
  }

  superJson.allowedErrorProps.forEach(prop => {
    (e as any)[prop] = v[prop];
  });

  return e;
}

function errorTransformation<A extends ErrorTypeAnnotation>(annotation: A) {
  return simpleTransformation(
    (v, superJson): v is Error =>
      isError(v) && errorAnnotationFor(v, superJson) === annotation,
    annotation,
    (v, superJson) => transformError(v, superJson, annotation),
    (v, superJson) => untransformError(v, superJson, annotation)
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

  errorTransformation('Error'),
  errorTransformation('Error/stack'),
  errorTransformation('Error/frames'),

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
