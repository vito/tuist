package tuist_test

import (
	"strings"

	"github.com/vito/tuist"
)

// ExampleCompletionMenu demonstrates how to wire up a CompletionMenu with
// a TextInput and a custom provider.
func Example_completionMenu() {
	ti := tuist.NewTextInput("sql> ")

	// Define available completions — in a real app these might come from
	// a schema, type environment, or static keyword list.
	keywords := []string{"SELECT", "FROM", "WHERE", "INSERT", "UPDATE", "DELETE", "JOIN", "LEFT", "RIGHT", "INNER", "GROUP", "ORDER", "BY", "LIMIT"}

	provider := func(input string, cursor int) tuist.CompletionResult {
		// Find the word being typed at the cursor.
		text := input[:cursor]
		wordStart := len(text)
		for wordStart > 0 && text[wordStart-1] != ' ' {
			wordStart--
		}
		partial := text[wordStart:]
		if partial == "" {
			return tuist.CompletionResult{}
		}

		partialUpper := strings.ToUpper(partial)
		var items []tuist.Completion
		for _, kw := range keywords {
			if strings.HasPrefix(kw, partialUpper) {
				items = append(items, tuist.Completion{
					Label:         kw,
					Detail:        "keyword",
					Documentation: "SQL keyword: " + kw,
					Kind:          "keyword",
				})
			}
		}
		return tuist.CompletionResult{
			Items:       items,
			ReplaceFrom: wordStart,
		}
	}

	_ = tuist.NewCompletionMenu(ti, provider)

	// In a real app:
	//   container.AddChild(ti)
	//   // CompletionMenu manages overlays via the TextInput's OnChange.
	//   // Parent HandleKeyPress should delegate to menu.HandleKeyPress first.
}
