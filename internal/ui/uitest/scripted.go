// Package uitest traz um Prompter roteirizado para os testes dos comandos.
package uitest

import (
	"fmt"

	"github.com/jataicloud/cli/internal/ui"
)

// ScriptedPrompter responde a partir de uma fila e registra as perguntas.
// Para Confirm, "y" é sim; qualquer outra resposta é não.
type ScriptedPrompter struct {
	Answers []string
	Asked   []string
}

func (s *ScriptedPrompter) next(question string) (string, error) {
	s.Asked = append(s.Asked, question)
	if len(s.Answers) == 0 {
		return "", fmt.Errorf("pergunta sem resposta roteirizada: %q", question)
	}
	answer := s.Answers[0]
	s.Answers = s.Answers[1:]
	return answer, nil
}

func (s *ScriptedPrompter) Confirm(question string) (bool, error) {
	answer, err := s.next(question)
	return answer == "y", err
}

func (s *ScriptedPrompter) Input(question string) (string, error) { return s.next(question) }

func (s *ScriptedPrompter) Select(question string, _ []ui.Option) (string, error) {
	return s.next(question)
}
