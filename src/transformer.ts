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
import { matchesClassFilter } from './error-options.js';
import { processStackFrames, processStackString } from './error-stack.js';
import { sanitizeMessage } from './error-sanitizer.js';

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

function errorStackAnnotation(
  v: Error,
  superJson: SuperJSON
): ErrorTypeAnnotation {
  const opts = superJson.errorStack;
  if (!opts || opts.mode === 'off' || !matchesClassFilter(opts, v.name)) {
    return 'Error';
  }
  return opts.mode === 'string' ? 'Error/stack' : 'Error/frames';
}

const RESERVED_ERROR_PROPS = ['stack', 'stackFrames', 'cause', 'errors'];

function serializeErrorNode(
  v: Error,
  superJson: SuperJSON,
  sanitize: boolean,
  depth: number,
  seen: Set<Error>
): any {
  const opts = superJson.errorStack!;
  seen.add(v);
  const matches = matchesClassFilter(opts, v.name);
  const out: any = {
    name: v.name,
    message: sanitize ? sanitizeMessage(v.message) : v.message,
  };

  const allowed = superJson.allowedErrorProps;
  if (matches && typeof v.stack === 'string') {
    if (opts.mode === 'string' && allowed.includes('stack')) {
      out.stack = processStackString(v.stack, opts);
    } else if (opts.mode === 'frames' && allowed.includes('stackFrames')) {
      out.stackFrames = processStackFrames(v.stack, opts);
    }
  }

  allowed.forEach(prop => {
    if (!RESERVED_ERROR_PROPS.includes(prop)) {
      out[prop] = (v as any)[prop];
    }
  });

  if (Array.isArray((v as any).errors) && v.name === 'AggregateError') {
    out.errors = (v as any).errors;
  }

  const maxDepth =
    opts.includeCauses === 'direct'
      ? 1
      : opts.includeCauses === 'deep'
      ? opts.maxCauseDepth
      : 0;
  const cause = (v as any).cause;
  if (depth < maxDepth && isError(cause) && !seen.has(cause)) {
    out.cause = serializeErrorNode(cause, superJson, sanitize, depth + 1, seen);
  }

  const processor = superJson.errorClassRegistry.getProcessor(v.name);
  return processor ? processor(out) : out;
}

function serializeConfiguredError(v: Error, superJson: SuperJSON) {
  const opts = superJson.errorStack!;
  const sanitize = opts.sanitizeMessage && matchesClassFilter(opts, v.name);
  return serializeErrorNode(v, superJson, sanitize, 0, new Set());
}

function deserializeConfiguredError(v: any, superJson: SuperJSON): Error {
  const cause =
    v.cause && typeof v.cause === 'object'
      ? deserializeConfiguredError(v.cause, superJson)
      : undefined;
  const options = cause ? { cause } : undefined;
  const e: any =
    v.name === 'AggregateError' &&
    Array.isArray(v.errors) &&
    typeof AggregateError !== 'undefined'
      ? new AggregateError(v.errors, v.message, options)
      : new Error(v.message, options);
  e.name = v.name;
  if (Array.isArray(v.stackFrames)) {
    e.stackFrames = v.stackFrames;
    e.stack = v.stackFrames.map((f: any) => f?.raw).join('\n');
  } else {
    e.stack = v.stack;
  }

  superJson.allowedErrorProps.forEach(prop => {
    if (!RESERVED_ERROR_PROPS.includes(prop)) {
      e[prop] = v[prop];
    }
  });

  return e;
}

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
    (v: any, superJson): v is Error =>
      isError(v) && errorStackAnnotation(v, superJson) === 'Error',
    'Error',
    (v, superJson) => {
      if (superJson.errorStack) {
        return serializeConfiguredError(v, superJson);
      }
      const baseError: any = {
        name: v.name,
        message: v.message,
      };

      if ('cause' in v) {
        baseError.cause = v.cause;
      }

      superJson.allowedErrorProps.forEach(prop => {
        baseError[prop] = (v as any)[prop];
      });

      return baseError;
    },
    (v, superJson) => {
      if (superJson.errorStack) {
        return deserializeConfiguredError(v, superJson);
      }
      const e = new Error(v.message, { cause: v.cause });
      e.name = v.name;
      e.stack = v.stack;

      superJson.allowedErrorProps.forEach(prop => {
        (e as any)[prop] = v[prop];
      });

      return e;
    }
  ),

  simpleTransformation(
    (v: any, superJson): v is Error =>
      isError(v) && errorStackAnnotation(v, superJson) === 'Error/stack',
    'Error/stack',
    (v, superJson) => serializeConfiguredError(v, superJson),
    (v, superJson) => deserializeConfiguredError(v, superJson)
  ),

  simpleTransformation(
    (v: any, superJson): v is Error =>
      isError(v) && errorStackAnnotation(v, superJson) === 'Error/frames',
    'Error/frames',
    (v, superJson) => serializeConfiguredError(v, superJson),
    (v, superJson) => deserializeConfiguredError(v, superJson)
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
