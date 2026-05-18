package tools

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// ============================================================
// HTTP Helper
// ============================================================

// NewHTTPRequest выполняет HTTP запрос и возвращает тело ответа
func NewHTTPRequest(ctx context.Context, method, url string) (string, error) {
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; VKBot/1.0)")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body[:min(len(body), 500)]))
	}

	return string(body), nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ============================================================
// Math Expression Evaluator
// ============================================================

// EvaluateExpression evaluates a simple arithmetic expression
// Supports: +, -, *, /, %, ** (power), ( ), pi, e, sin, cos, tan, sqrt, abs, round, floor, ceil
func EvaluateExpression(expr string) (float64, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return 0, fmt.Errorf("empty expression")
	}

	// Replace constants
	expr = strings.ReplaceAll(expr, "pi", fmt.Sprintf("%.15f", math.Pi))
	expr = strings.ReplaceAll(expr, "e", fmt.Sprintf("%.15f", math.E))

	tokens, err := tokenize(expr)
	if err != nil {
		return 0, err
	}

	result, err := parseExpression(tokens)
	if err != nil {
		return 0, err
	}

	return result, nil
}

type tokenType int

const (
	tokenNumber tokenType = iota
	tokenPlus
	tokenMinus
	tokenMul
	tokenDiv
	tokenMod
	tokenPow
	tokenLParen
	tokenRParen
	tokenFunc
	tokenComma
)

type token struct {
	typ   tokenType
	value string
	fval  float64
}

func tokenize(expr string) ([]token, error) {
	var tokens []token
	runes := []rune(expr)
	i := 0

	for i < len(runes) {
		ch := runes[i]

		if unicode.IsSpace(ch) {
			i++
			continue
		}

		if unicode.IsDigit(ch) || ch == '.' {
			start := i
			for i < len(runes) && (unicode.IsDigit(runes[i]) || runes[i] == '.') {
				i++
			}
			val, err := strconv.ParseFloat(string(runes[start:i]), 64)
			if err != nil {
				return nil, fmt.Errorf("invalid number: %s", string(runes[start:i]))
			}
			tokens = append(tokens, token{typ: tokenNumber, fval: val})
			continue
		}

		if unicode.IsLetter(ch) || ch == '_' {
			start := i
			for i < len(runes) && (unicode.IsLetter(runes[i]) || unicode.IsDigit(runes[i]) || runes[i] == '_') {
				i++
			}
			funcName := string(runes[start:i])
			known := map[string]bool{
				"sin": true, "cos": true, "tan": true,
				"sqrt": true, "abs": true, "round": true, "floor": true, "ceil": true,
			}
			if known[funcName] {
				tokens = append(tokens, token{typ: tokenFunc, value: funcName})
			} else {
				return nil, fmt.Errorf("unknown function: %s", funcName)
			}
			continue
		}

		switch ch {
		case '+':
			tokens = append(tokens, token{typ: tokenPlus})
		case '-':
			tokens = append(tokens, token{typ: tokenMinus})
		case '*':
			if i+1 < len(runes) && runes[i+1] == '*' {
				tokens = append(tokens, token{typ: tokenPow})
				i++
			} else {
				tokens = append(tokens, token{typ: tokenMul})
			}
		case '/':
			tokens = append(tokens, token{typ: tokenDiv})
		case '%':
			tokens = append(tokens, token{typ: tokenMod})
		case '(':
			tokens = append(tokens, token{typ: tokenLParen})
		case ')':
			tokens = append(tokens, token{typ: tokenRParen})
		case ',':
			tokens = append(tokens, token{typ: tokenComma})
		default:
			return nil, fmt.Errorf("unexpected character: %c", ch)
		}
		i++
	}

	return tokens, nil
}

// Recursive descent parser
// Grammar:
//   expression = term (("+" | "-") term)*
//   term = factor (("*" | "/" | "%") factor)*
//   factor = unary ("**" factor)?
//   unary = ("-" | "+") unary | primary
//   primary = number | "(" expression ")" | func "(" expression ")"

func parseExpression(tokens []token) (float64, error) {
	result, tokens, err := parseAddSub(tokens, 0)
	if err != nil {
		return 0, err
	}
	if len(tokens) > 0 {
		return 0, fmt.Errorf("unexpected token after expression")
	}
	return result, nil
}

func parseAddSub(tokens []token, minPrec int) (float64, []token, error) {
	left, tokens, err := parseMulDiv(tokens, minPrec+1)
	if err != nil {
		return 0, tokens, err
	}

	for len(tokens) > 0 {
		typ := tokens[0].typ
		if typ != tokenPlus && typ != tokenMinus {
			break
		}
		tokens = tokens[1:]

		right, rest, err := parseMulDiv(tokens, minPrec+1)
		if err != nil {
			return 0, rest, err
		}
		tokens = rest

		if typ == tokenPlus {
			left += right
		} else {
			left -= right
		}
	}

	return left, tokens, nil
}

func parseMulDiv(tokens []token, minPrec int) (float64, []token, error) {
	left, tokens, err := parsePower(tokens, minPrec+1)
	if err != nil {
		return 0, tokens, err
	}

	for len(tokens) > 0 {
		typ := tokens[0].typ
		if typ != tokenMul && typ != tokenDiv && typ != tokenMod {
			break
		}
		tokens = tokens[1:]

		right, rest, err := parsePower(tokens, minPrec+1)
		if err != nil {
			return 0, rest, err
		}
		tokens = rest

		switch typ {
		case tokenMul:
			left *= right
		case tokenDiv:
			if right == 0 {
				return 0, tokens, fmt.Errorf("division by zero")
			}
			left /= right
		case tokenMod:
			if right == 0 {
				return 0, tokens, fmt.Errorf("modulo by zero")
			}
			left = float64(int64(left) % int64(right))
		}
	}

	return left, tokens, nil
}

func parsePower(tokens []token, minPrec int) (float64, []token, error) {
	left, tokens, err := parseUnary(tokens, minPrec+1)
	if err != nil {
		return 0, tokens, err
	}

	if len(tokens) > 0 && tokens[0].typ == tokenPow {
		tokens = tokens[1:]
		right, rest, err := parsePower(tokens, minPrec+1)
		if err != nil {
			return 0, rest, err
		}
		tokens = rest
		left = math.Pow(left, right)
	}

	return left, tokens, nil
}

func parseUnary(tokens []token, minPrec int) (float64, []token, error) {
	if len(tokens) == 0 {
		return 0, tokens, fmt.Errorf("unexpected end of expression")
	}

	if tokens[0].typ == tokenMinus {
		tokens = tokens[1:]
		val, rest, err := parseUnary(tokens, minPrec+1)
		if err != nil {
			return 0, rest, err
		}
		return -val, rest, nil
	}

	if tokens[0].typ == tokenPlus {
		tokens = tokens[1:]
		return parseUnary(tokens, minPrec+1)
	}

	return parsePrimary(tokens)
}

func parsePrimary(tokens []token) (float64, []token, error) {
	if len(tokens) == 0 {
		return 0, tokens, fmt.Errorf("unexpected end of expression")
	}

	tok := tokens[0]
	rest := tokens[1:]

	switch tok.typ {
	case tokenNumber:
		return tok.fval, rest, nil

	case tokenLParen:
		val, rest, err := parseAddSub(rest, 0)
		if err != nil {
			return 0, rest, err
		}
		if len(rest) == 0 || rest[0].typ != tokenRParen {
			return 0, rest, fmt.Errorf("missing closing parenthesis")
		}
		return val, rest[1:], nil

	case tokenFunc:
		if len(rest) == 0 || rest[0].typ != tokenLParen {
			return 0, rest, fmt.Errorf("expected ( after function %s", tok.value)
		}
		rest = rest[1:]

		arg, rest, err := parseAddSub(rest, 0)
		if err != nil {
			return 0, rest, err
		}
		if len(rest) == 0 || rest[0].typ != tokenRParen {
			return 0, rest, fmt.Errorf("missing ) after function %s arguments", tok.value)
		}
		rest = rest[1:]

		var result float64
		switch tok.value {
		case "sin":
			result = math.Sin(arg)
		case "cos":
			result = math.Cos(arg)
		case "tan":
			result = math.Tan(arg)
		case "sqrt":
			result = math.Sqrt(arg)
		case "abs":
			result = math.Abs(arg)
		case "round":
			result = math.Round(arg)
		case "floor":
			result = math.Floor(arg)
		case "ceil":
			result = math.Ceil(arg)
		default:
			return 0, rest, fmt.Errorf("unknown function: %s", tok.value)
		}
		return result, rest, nil

	default:
		return 0, tokens, fmt.Errorf("unexpected token")
	}
}
