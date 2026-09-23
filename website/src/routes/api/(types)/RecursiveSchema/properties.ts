import type { PropertyProps } from '~/components';

export const properties: Record<string, PropertyProps> = {
  TSchema: {
    modifier: 'extends',
    type: {
      type: 'custom',
      name: 'BaseSchema',
      href: '../BaseSchema/',
      generics: [
        'unknown',
        'unknown',
        {
          type: 'custom',
          name: 'BaseIssue',
          href: '../BaseIssue/',
          generics: ['unknown'],
        },
      ],
    },
  },
  BaseSchema: {
    modifier: 'extends',
    type: {
      type: 'custom',
      name: 'BaseSchema',
      href: '../BaseSchema/',
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
      name: 'recursive',
      href: '../recursive/',
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
