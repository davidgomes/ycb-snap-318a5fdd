import type { PropertyProps } from '~/components';

const baseIssue: PropertyProps['type'] = {
  type: 'custom',
  name: 'BaseIssue',
  href: '../BaseIssue/',
  generics: ['unknown'],
};

const baseSchema: PropertyProps['type'] = {
  type: 'custom',
  name: 'BaseSchema',
  href: '../BaseSchema/',
  generics: ['unknown', 'unknown', baseIssue],
};

const baseSchemaAsync: PropertyProps['type'] = {
  type: 'custom',
  name: 'BaseSchemaAsync',
  href: '../BaseSchemaAsync/',
  generics: ['unknown', 'unknown', baseIssue],
};

export const properties: Record<string, PropertyProps> = {
  TSchema: {
    modifier: 'extends',
    type: {
      type: 'union',
      options: [baseSchema, baseSchemaAsync],
    },
  },
  schema: {
    type: {
      type: 'custom',
      name: 'TSchema',
    },
  },
  Schema: {
    type: {
      type: 'custom',
      name: 'RecursiveSchemaAsync',
      href: '../RecursiveSchemaAsync/',
      generics: [
        {
          type: 'custom',
          name: 'TSchema',
        },
      ],
    },
  },
};
