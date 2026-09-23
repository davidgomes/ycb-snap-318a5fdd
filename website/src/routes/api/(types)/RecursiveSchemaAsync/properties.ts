import type { PropertyProps } from '~/components';

const baseIssue: PropertyProps['type'] = {
  type: 'custom',
  name: 'BaseIssue',
  href: '../BaseIssue/',
  generics: ['unknown'],
};

export const properties: Record<string, PropertyProps> = {
  TSchema: {
    modifier: 'extends',
    type: {
      type: 'union',
      options: [
        {
          type: 'custom',
          name: 'BaseSchema',
          href: '../BaseSchema/',
          generics: ['unknown', 'unknown', baseIssue],
        },
        {
          type: 'custom',
          name: 'BaseSchemaAsync',
          href: '../BaseSchemaAsync/',
          generics: ['unknown', 'unknown', baseIssue],
        },
      ],
    },
  },
  BaseSchemaAsync: {
    modifier: 'extends',
    type: {
      type: 'custom',
      name: 'BaseSchemaAsync',
      href: '../BaseSchemaAsync/',
      generics: [
        {
          type: 'custom',
          name: 'InferRecursiveInput',
          href: '../InferRecursiveInput/',
          generics: [
            {
              type: 'custom',
              name: 'TSchema',
            },
          ],
        },
        {
          type: 'custom',
          name: 'InferRecursiveOutput',
          href: '../InferRecursiveOutput/',
          generics: [
            {
              type: 'custom',
              name: 'TSchema',
            },
          ],
        },
        {
          type: 'custom',
          name: 'InferIssue',
          href: '../InferIssue/',
          generics: [
            {
              type: 'custom',
              name: 'TSchema',
            },
          ],
        },
      ],
    },
  },
  type: {
    type: {
      type: 'string',
      value: 'recursive',
    },
  },
  reference: {
    type: {
      type: 'custom',
      modifier: 'typeof',
      name: 'recursiveAsync',
      href: '../recursiveAsync/',
    },
  },
  expects: {
    type: {
      type: 'custom',
      name: 'TSchema',
      indexes: [
        {
          type: 'string',
          value: 'expects',
        },
      ],
    },
  },
  wrapped: {
    type: {
      type: 'custom',
      name: 'TSchema',
    },
  },
};
