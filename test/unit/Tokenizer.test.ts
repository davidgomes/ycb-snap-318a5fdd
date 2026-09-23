import Tokenizer from '../../src/lexer/Tokenizer.js';

describe('Tokenizer', () => {
  const tokenize = (sql: string) =>
    new Tokenizer(
      {
        reservedClauses: ['FROM', 'WHERE', 'LIMIT', 'CREATE TABLE'],
        reservedSelect: ['SELECT'],
        reservedSetOperations: ['UNION', 'UNION ALL'],
        reservedJoins: ['JOIN'],
        reservedFunctionNames: ['SQRT', 'CURRENT_TIME'],
        reservedKeywords: ['BETWEEN', 'LIKE', 'ON', 'USING'],
        reservedDataTypes: [],
        stringTypes: ["''-qq"],
        identTypes: ['""-qq'],
      },
      'sql'
    ).tokenize(sql, {});

  it('tokenizes whitespace to empty array', () => {
    expect(tokenize(' \t\n \n\r ')).toEqual([]);
  });

  it('tokenizes single line SQL tokens', () => {
    expect(tokenize('SELECT * FROM foo;')).toMatchSnapshot();
  });

  it('tokenizes multiline SQL tokens', () => {
    expect(tokenize('SELECT "foo\n bar" /* \n\n\n */;')).toMatchSnapshot();
  });

  describe('pipe syntax', () => {
    const tokenizePipes = (sql: string, supportsPipeSyntax: boolean) =>
      new Tokenizer(
        {
          reservedClauses: ['FROM', 'WHERE'],
          reservedSelect: ['SELECT'],
          reservedSetOperations: [],
          reservedJoins: [],
          reservedFunctionNames: [],
          reservedKeywords: [],
          reservedDataTypes: [],
          stringTypes: ["''-qq"],
          identTypes: ['""-qq'],
          operators: ['|', '||'],
          supportsPipeSyntax,
        },
        'sql'
      )
        .tokenize(sql, {})
        .map(({ type, text }) => [type, text]);

    it('tokenizes |> as a single PIPE_OPERATOR token', () => {
      expect(tokenizePipes('FROM t |> WHERE x', true)).toEqual([
        ['RESERVED_CLAUSE', 'FROM'],
        ['IDENTIFIER', 't'],
        ['PIPE_OPERATOR', '|>'],
        ['RESERVED_CLAUSE', 'WHERE'],
        ['IDENTIFIER', 'x'],
      ]);
    });

    it('tokenizes |> as separate operators when pipe syntax is not supported', () => {
      expect(tokenizePipes('t |> x', false)).toEqual([
        ['IDENTIFIER', 't'],
        ['OPERATOR', '|'],
        ['OPERATOR', '>'],
        ['IDENTIFIER', 'x'],
      ]);
    });

    it('does not treat | > separated by whitespace as pipe', () => {
      expect(tokenizePipes('a | > b || c', true)).toEqual([
        ['IDENTIFIER', 'a'],
        ['OPERATOR', '|'],
        ['OPERATOR', '>'],
        ['IDENTIFIER', 'b'],
        ['OPERATOR', '||'],
        ['IDENTIFIER', 'c'],
      ]);
    });
  });
});
