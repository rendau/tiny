package compat

import "strings"

const (
	diffContext  = 2   // сколько одинаковых строк показывать вокруг отличия
	diffMaxLines = 400 // длиннее — сравнение строк слишком дорогое, показываем оба текста целиком
	diffMaxWidth = 300 // длинные строки в выводе обрезаются
)

// lineDiff — построчное сравнение двух текстов: "- " ждали, "+ " пришло, "  " совпало.
func lineDiff(want, have string) string {
	a := strings.Split(strings.TrimRight(want, "\n"), "\n")
	b := strings.Split(strings.TrimRight(have, "\n"), "\n")

	if len(a) > diffMaxLines || len(b) > diffMaxLines {
		return "- " + clip(want) + "\n+ " + clip(have)
	}

	// длина наибольшей общей подпоследовательности для хвостов a[i:] и b[j:]
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}

	type line struct {
		mark byte
		text string
	}
	var lines []line

	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			lines = append(lines, line{' ', a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			lines = append(lines, line{'-', a[i]})
			i++
		default:
			lines = append(lines, line{'+', b[j]})
			j++
		}
	}
	for ; i < len(a); i++ {
		lines = append(lines, line{'-', a[i]})
	}
	for ; j < len(b); j++ {
		lines = append(lines, line{'+', b[j]})
	}

	// оставляем отличия и немного строк вокруг
	keep := make([]bool, len(lines))
	for idx, l := range lines {
		if l.mark == ' ' {
			continue
		}
		for k := max(0, idx-diffContext); k <= min(len(lines)-1, idx+diffContext); k++ {
			keep[k] = true
		}
	}

	var sb strings.Builder
	skipped := false
	for idx, l := range lines {
		if !keep[idx] {
			skipped = true
			continue
		}
		if skipped {
			sb.WriteString("  …\n")
			skipped = false
		}
		sb.WriteByte(l.mark)
		sb.WriteByte(' ')
		sb.WriteString(clip(l.text))
		sb.WriteByte('\n')
	}
	if skipped {
		sb.WriteString("  …\n")
	}

	return sb.String()
}

func clip(s string) string {
	runes := []rune(s)
	if len(runes) <= diffMaxWidth {
		return s
	}
	return string(runes[:diffMaxWidth]) + "… (обрезано)"
}
