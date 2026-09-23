import Tokenizer from '../../src/lexer/Tokenizer.js';
import { TokenizerOptions } from '../../src/lexer/TokenizerOptions.js';
import { TokenType } from '../../src/lexer/token.js';

describe('Tokenizer', () => {
  const tokenize = (sql: string, options: Partial<TokenizerOptions> = {}) =>
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
        ...options,
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

  it('tokenizes |> as PIPE when pipe syntax is supported', () => {
    const tokens = tokenize('FROM foo |> WHERE x', { operators: ['|'], supportsPipeSyntax: true });
    expect(tokens.map(({ type, text }) => [type, text])).toEqual([
      [TokenType.RESERVED_CLAUSE, 'FROM'],
      [TokenType.IDENTIFIER, 'foo'],
      [TokenType.PIPE, '|>'],
      [TokenType.RESERVED_CLAUSE, 'WHERE'],
      [TokenType.IDENTIFIER, 'x'],
    ]);
  });

  it('tokenizes |> as separate operators when pipe syntax is not supported', () => {
    const tokens = tokenize('a |> b', { operators: ['|'] });
    expect(tokens.map(({ type, text }) => [type, text])).toEqual([
      [TokenType.IDENTIFIER, 'a'],
      [TokenType.OPERATOR, '|'],
      [TokenType.OPERATOR, '>'],
      [TokenType.IDENTIFIER, 'b'],
    ]);
  });
});
