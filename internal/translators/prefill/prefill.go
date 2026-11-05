// Package prefill — перепаковка assistant-prefill для шлюзов, отвергающих
// финальное assistant-сообщение (GitHub Copilot: 400 "must end with a user
// message"). Точный порт rewrite_body из copilot-bridge.py: прецеденты и
// формулировки инструкций сохранены 1:1 — они выверены на живом шлюзе.
package prefill

import "strings"

// Instruction — текст инструкции продолжения (1:1 с bridge).

const (
	blockTypeText = "text"
	Instruction   = "[continuation] Предыдущий ответ оборвался на тексте ниже. " +
		"Продолжи его точно с места обрыва: не повторяй написанное, не комментируй, " +
		"сразу продолжение:\n\n"
)

// NoTextStub — заглушка, когда в префилле не было текста (thinking/пусто).
const NoTextStub = "(предыдущий ответ оборвался без текста — просто продолжи его с места остановки)"

// Rewrite перепаковывает prefill-запрос. Политика: последнее сообщение ОТ
// assistant любого вида (текст/thinking/пусто/tool_use) никогда не уходит
// upstream — заменяется финальным user с инструкцией продолжить; thinking и
// tool_use не переносятся. Возвращает (новый body, note); вход не мутируется.
func Rewrite(body map[string]any) (map[string]any, string) {
	rawMsgs, ok := body["messages"].([]any)
	if !ok || len(rawMsgs) == 0 {
		return body, "no-messages"
	}
	msgs := asMaps(rawMsgs)

	// Claude Code вставляет system-хук-контекст В КОНЕЦ messages, в т.ч.
	// после assistant-префилла; ищем assistant перед хвостом из system.
	k := len(msgs)
	for k > 0 && role(msgs[k-1]) == "system" {
		k--
	}
	systemTail := msgs[k:]

	if k == 0 || role(msgs[k-1]) != "assistant" {
		return rewriteNonAssistantFinal(body, msgs, k, systemTail)
	}

	last := msgs[k-1]
	texts, kinds := prefillContent(last["content"])
	note := "prefill:" + strings.Join(kinds, "+")
	if len(systemTail) > 0 {
		note += "+sys-tail:" + itoa(len(systemTail))
	}

	payload := Instruction
	if len(texts) > 0 {
		payload += strings.Join(texts, "\n")
	} else {
		payload += NoTextStub
	}

	// system-хвост сохраняем, но ставим ПЕРЕД финальным user (шлюз требует
	// user-финал; хук-контекст остаётся в разговоре)
	fresh := copyBody(body)
	fresh["messages"] = appendMaps(msgs[:k-1], systemTail,
		map[string]any{"role": "user", "content": payload})

	return fresh, note
}

// rewriteNonAssistantFinal — финал не assistant: no-op, либо слив system-хвоста
// в user (шлюз требует СТРОГО user-финал — [user, system] тоже отвергается).
func rewriteNonAssistantFinal(body map[string]any, msgs []map[string]any, k int, tail []map[string]any) (map[string]any, string) {
	if len(tail) == 0 {
		return body, "no-prefill"
	}
	sysTexts := systemTexts(tail)

	if k > 0 && role(msgs[k-1]) == "user" && len(sysTexts) > 0 {
		fresh := copyBody(body)
		fresh["messages"] = appendMaps(msgs[:k-1], nil, mergeIntoUser(msgs[k-1], sysTexts))

		return fresh, "sys-tail-merged:" + itoa(len(tail))
	}
	if k == 0 && len(sysTexts) > 0 {
		// messages — только system: всё в одно user-сообщение
		fresh := copyBody(body)
		fresh["messages"] = []any{
			map[string]any{"role": "user", "content": strings.Join(sysTexts, "\n\n")},
		}

		return fresh, "sys-only:" + itoa(len(tail))
	}

	return body, "no-prefill"
}

// prefillContent извлекает тексты и виды блоков assistant-префилла.
//
//nolint:gocognit // точный порт rewrite_body (1:1 с copilot-bridge.py)
func prefillContent(content any) (texts, kinds []string) {
	switch c := content.(type) {
	case string:
		if c != "" {
			texts = append(texts, c)
			kinds = append(kinds, "text")
		} else {
			kinds = append(kinds, "empty")
		}
	case []any:
		for _, raw := range c {
			block, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			blockType, _ := block["type"].(string) //nolint:revive // дефолт "?" ниже
			if blockType == "" {
				blockType = "?"
			}
			kinds = append(kinds, blockType)
			if blockType == blockTypeText {
				if text, _ := block["text"].(string); text != "" { //nolint:revive // пустая строка == нет текста
					texts = append(texts, text)
				}
			}
		}
	default:
		// прочие типы контента — только в kinds
	}
	if len(kinds) == 0 {
		kinds = []string{"empty"}
	}

	return texts, kinds
}

// systemTexts — тексты system-сообщений (строка или text-блоки).
//
//nolint:gocognit // точный порт _system_texts из copilot-bridge.py (1:1)
func systemTexts(systemMsgs []map[string]any) []string {
	var texts []string
	for _, m := range systemMsgs {
		switch c := m["content"].(type) {
		case string:
			if c != "" {
				texts = append(texts, c)
			}
		case []any:
			for _, raw := range c {
				if block, ok := raw.(map[string]any); ok {
					if block["type"] == blockTypeText {
						if text, _ := block["text"].(string); text != "" { //nolint:revive // пустая строка == нет текста //nolint:revive // пустая строка == нет текста
							texts = append(texts, text)
						}
					}
				}
			}
		default:
			// прочий контент игнорируем
		}
	}

	return texts
}

// mergeIntoUser дописывает system-тексты в user-сообщение (строка или блоки).
func mergeIntoUser(userMsg map[string]any, sysTexts []string) map[string]any {
	merged := copyMap(userMsg)
	add := strings.Join(sysTexts, "\n\n")
	switch c := userMsg["content"].(type) {
	case string:
		if c != "" {
			merged["content"] = c + "\n\n[system context]\n" + add
		} else {
			merged["content"] = "[system context]\n" + add
		}
	case []any:
		merged["content"] = append(append([]any{}, c...),
			map[string]any{"type": "text", "text": "[system context]\n" + add})
	default:
		merged["content"] = "[system context]\n" + add
	}

	return merged
}

func role(msg map[string]any) string {
	r, _ := msg["role"].(string) //nolint:revive // нет роли == пустая строка

	return r
}

func asMaps(items []any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}

	return out
}

// appendMaps собирает map-сообщения в []any (порядок: a, b, tail...).
func appendMaps(a, b []map[string]any, tail ...map[string]any) []any {
	out := make([]any, 0, len(a)+len(b)+len(tail))
	for _, m := range a {
		out = append(out, m)
	}
	for _, m := range b {
		out = append(out, m)
	}
	for _, m := range tail {
		out = append(out, m)
	}

	return out
}

func copyBody(body map[string]any) map[string]any {
	fresh := make(map[string]any, len(body)+1)
	for k, v := range body {
		fresh[k] = v
	}

	return fresh
}

func copyMap(m map[string]any) map[string]any {
	fresh := make(map[string]any, len(m)+1)
	for k, v := range m {
		fresh[k] = v
	}

	return fresh
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}

	return digits
}
