package convert

import (
	"errors"
	"math"
	"strings"
	"unicode"
)

// Limits for arithmetic in an amount, so a message cannot make the parser do
// unbounded work.
const (
	// MaxExpressionLength is the longest expression in runes, from its first
	// number or parenthesis to its last one.
	MaxExpressionLength = 200
	// MaxExpressionDepth is how deeply parentheses may be nested.
	MaxExpressionDepth = 10
)

var (
	ErrDivisionByZero       = errors.New("division by zero")
	ErrNegativeAmount       = errors.New("amount is negative")
	ErrExpressionTooComplex = errors.New("expression is too long or too deeply nested")
	errNotExpression        = errors.New("not an expression")
)

type exprKind int

const (
	exprNumber exprKind = iota
	exprOperator
	exprOpen
	exprClose
	exprWord
	exprOther
)

type exprToken struct {
	kind exprKind
	// text is the number without group spaces.
	text string
	// op is the operator normalized to one of + - * /.
	op rune
	// glued means the operator has no spaces on either side, as in "12-05".
	glued      bool
	start, end int
}

// parseExpression evaluates arithmetic in a line: "100+50 usd", "(12*3)+5",
// "1 000 / 4", "70 х 5 литров молока". It returns errNotExpression when the
// line is not one expression, so the caller can read it the old way; other
// errors mean the line is an expression that cannot be evaluated.
func parseExpression(input string, opts Options) (float64, error) {
	runes := []rune(input)
	run, ok := expressionRun(tokenizeExpression(runes))
	if !ok || looksLikeDateOrPhone(run) {
		return 0, errNotExpression
	}
	if run[len(run)-1].end-run[0].start > MaxExpressionLength {
		return 0, ErrExpressionTooComplex
	}

	p := exprParser{tokens: run, opts: opts}
	node, err := p.parseSum(0)
	if err != nil {
		return 0, err
	}
	if p.pos != len(p.tokens) {
		return 0, errNotExpression
	}
	value, err := node.eval()
	if err != nil {
		return 0, err
	}
	switch {
	case math.IsNaN(value) || math.IsInf(value, 0) || value > MaxAmount:
		return 0, ErrTooLarge
	case value < 0:
		return 0, ErrNegativeAmount
	}
	return value, nil
}

func tokenizeExpression(runes []rune) []exprToken {
	var tokens []exprToken
	for i := 0; i < len(runes); {
		r := runes[i]
		start := i
		switch {
		case unicode.IsSpace(r) || IsCurrencySymbol(r):
			// "100$ + 50$": currency symbols do not break an expression.
			i++
			continue
		case unicode.IsDigit(r):
			var text string
			text, i = scanNumber(runes, i)
			tokens = append(tokens, exprToken{kind: exprNumber, text: text, start: start, end: i})
			continue
		case r == '(':
			tokens = append(tokens, exprToken{kind: exprOpen})
		case r == ')':
			tokens = append(tokens, exprToken{kind: exprClose})
		case operatorAt(runes, i) != 0:
			glued := i > 0 && i+1 < len(runes) && !unicode.IsSpace(runes[i-1]) && !unicode.IsSpace(runes[i+1])
			tokens = append(tokens, exprToken{kind: exprOperator, op: operatorAt(runes, i), glued: glued})
		case unicode.IsLetter(r):
			for i < len(runes) && (unicode.IsLetter(runes[i]) || runes[i] == '\'' || runes[i] == '’') {
				i++
			}
			tokens = append(tokens, exprToken{kind: exprWord, start: start, end: i})
			continue
		default:
			tokens = append(tokens, exprToken{kind: exprOther})
		}
		i++
		tokens[len(tokens)-1].start, tokens[len(tokens)-1].end = start, i
	}
	return tokens
}

// operatorAt returns the operator at runes[i], or 0. The letters x and х are
// operators only on their own ("100 x 9", "100х9"), not inside a word ("box").
func operatorAt(runes []rune, i int) rune {
	switch runes[i] {
	case '+':
		return '+'
	case '-', '−':
		return '-'
	case '*', '×':
		return '*'
	case '/', '÷':
		return '/'
	}
	letterAt := func(j int) bool { return j >= 0 && j < len(runes) && unicode.IsLetter(runes[j]) }
	if isMultiplicationSign(runes[i]) && !letterAt(i-1) && !letterAt(i+1) {
		return '*'
	}
	return 0
}

// expressionRun returns the only stretch of numbers, operators and
// parentheses in tokens that contains numbers, if it has at least one
// operator. A single word between a number and an operator stays part of the
// stretch ("100 usd + 50", "100 usd x 9"); any other word or symbol ends it.
// Several stretches with numbers ("iPhone 15 за 1000") are not an expression.
func expressionRun(tokens []exprToken) ([]exprToken, bool) {
	var found, current []exprToken
	runs := 0
	flush := func() {
		if hasKind(current, exprNumber) {
			runs++
			found = current
		}
		current = nil
	}
	for i, t := range tokens {
		switch t.kind {
		case exprNumber, exprOperator, exprOpen, exprClose:
			current = append(current, t)
		case exprWord:
			last := len(current) - 1
			betweenNumberAndOperator := last >= 0 && (current[last].kind == exprNumber || current[last].kind == exprClose) &&
				i+1 < len(tokens) && tokens[i+1].kind == exprOperator
			if !betweenNumberAndOperator {
				flush()
			}
		default:
			flush()
		}
	}
	flush()
	if runs != 1 || !hasKind(found, exprOperator) {
		return nil, false
	}
	return found, true
}

func hasKind(tokens []exprToken, kind exprKind) bool {
	for _, t := range tokens {
		if t.kind == kind {
			return true
		}
	}
	return false
}

// looksLikeDateOrPhone reports runs where "-" and "/" written without spaces
// separate the parts of a date, phone number or range rather than subtract or
// divide: "12-05-2024", "8-800-555-35-35", "12/05", "10-15 usd". Spaced
// operators ("100 - 50") and parentheses keep the run an expression.
func looksLikeDateOrPhone(run []exprToken) bool {
	onlyGluedMinus := true
	for i, t := range run {
		switch t.kind {
		case exprOpen, exprClose:
			onlyGluedMinus = false
		case exprOperator:
			if !t.glued || t.op != '-' {
				onlyGluedMinus = false
			}
			if !t.glued || (t.op != '-' && t.op != '/') {
				continue
			}
			// "05" in "12/05" or "2024-05-12" is a date part.
			if i > 0 && hasLeadingZero(run[i-1]) || i+1 < len(run) && hasLeadingZero(run[i+1]) {
				return true
			}
			// "12/05/2024", "123-45-67": the same separator chained.
			if i+2 < len(run) && run[i+1].kind == exprNumber && run[i+2].kind == exprOperator && run[i+2].glued && run[i+2].op == t.op {
				return true
			}
		}
	}
	return onlyGluedMinus
}

func hasLeadingZero(t exprToken) bool {
	return t.kind == exprNumber && len(t.text) > 1 && t.text[0] == '0' && t.text[1] >= '0' && t.text[1] <= '9'
}

type exprNode struct {
	op          rune
	value       float64
	left, right *exprNode
}

func (n *exprNode) eval() (float64, error) {
	if n.op == 0 {
		return n.value, nil
	}
	left, err := n.left.eval()
	if err != nil {
		return 0, err
	}
	right, err := n.right.eval()
	if err != nil {
		return 0, err
	}
	switch n.op {
	case '+':
		return left + right, nil
	case '-':
		return left - right, nil
	case '*':
		return left * right, nil
	default:
		if right == 0 {
			return 0, ErrDivisionByZero
		}
		return left / right, nil
	}
}

// exprParser is a recursive-descent parser with the usual precedence:
//
//	sum     = product { ("+" | "-") product }
//	product = primary { ("*" | "/") primary }
//	primary = number | "(" sum ")"
//
// There is no unary minus: amounts are not negative, and "-100" is read as 100
// by the plain parser as before.
type exprParser struct {
	tokens []exprToken
	pos    int
	opts   Options
}

func (p *exprParser) parseSum(depth int) (*exprNode, error) {
	return p.parseBinary(depth, "+-", p.parseProduct)
}

func (p *exprParser) parseProduct(depth int) (*exprNode, error) {
	return p.parseBinary(depth, "*/", p.parsePrimary)
}

func (p *exprParser) parseBinary(depth int, ops string, operand func(int) (*exprNode, error)) (*exprNode, error) {
	left, err := operand(depth)
	if err != nil {
		return nil, err
	}
	for p.pos < len(p.tokens) {
		t := p.tokens[p.pos]
		if t.kind != exprOperator || !strings.ContainsRune(ops, t.op) {
			break
		}
		p.pos++
		right, err := operand(depth)
		if err != nil {
			return nil, err
		}
		left = &exprNode{op: t.op, left: left, right: right}
	}
	return left, nil
}

func (p *exprParser) parsePrimary(depth int) (*exprNode, error) {
	if p.pos >= len(p.tokens) {
		return nil, errNotExpression
	}
	t := p.tokens[p.pos]
	switch t.kind {
	case exprNumber:
		p.pos++
		value, err := parseNumber(t.text, p.opts)
		if errors.Is(err, errNotNumber) {
			return nil, errNotExpression
		}
		if err != nil {
			return nil, err
		}
		return &exprNode{value: value}, nil
	case exprOpen:
		if depth >= MaxExpressionDepth {
			return nil, ErrExpressionTooComplex
		}
		p.pos++
		node, err := p.parseSum(depth + 1)
		if err != nil {
			return nil, err
		}
		if p.pos >= len(p.tokens) || p.tokens[p.pos].kind != exprClose {
			return nil, errNotExpression
		}
		p.pos++
		return node, nil
	default:
		return nil, errNotExpression
	}
}
