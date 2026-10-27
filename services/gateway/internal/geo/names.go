package geo

import (
	"bufio"
	"io"
	"strconv"
	"strings"
	"unicode"
)

// suffixes are the generic words GeoNames puts after the name of a division ("Adıyaman Province", "Kadıköy İlçesi", "Cook County"). They say what the
// division is, not what it is called, and an address does not carry them, so Build drops them. Only trailing words are dropped, and only while a name is
// left. Islands, atolls, cities and "Republic of" are not listed: "Rhode Island" and "Quezon City" are the names themselves.
var suffixes = []string{
	"Province", "Region", "County", "Department", "District", "Oblast", "State", "Governorate", "Municipality", "Parish", "Prefecture", "Voivodeship",
	"Krai", "Okrug", "Rayon", "Raion", "Subregion", "Zone", "Division", "Republic", "Town", "Commune", "Canton", "Borough",
	"İlçesi", "ilçesi", "Ilcesi", "ilcesi", "Shi", "Gun", "Kommun", "Kommune", "kommun", "pagasts", "novads", "vald", "Járás", "Tumani", "Shahri",
	"Barrio", "Barrio-Pueblo", "Administratsiya", "Aūdany",
}

// prefixes are the generic words some names start with ("State of Alabama", "Région Bretagne", "Land Niedersachsen"), dropped like the suffixes.
var prefixes = []string{
	"Free State of ", "Freie und Hansestadt ", "Freie Hansestadt ", "Hansestadt ", "State of ", "Land ", "Région ", "Region ", "Province of ", "Provincia de ",
	"Provincia di ", "County of ", "Municipality of ", "Municipio de ", "Department of ", "Departamento de ", "Governorate of ", "Canton of ", "Canton de ",
	"Kanton ", "Regione Autonoma ", "Regione ", "Principado de ", "Comunidad Foral de ", "Comunidad Autónoma del ", "Región de ", "Región del ", "Autonomous Community of ", "Comunidad Autónoma de ", "Comunidad de ", "Comunitat ", "Provincie ", "Provinz ", "Prefecture of ",
	"Département de l'", "Département de ", "Département du ", "Département des ", "Département d'", "Comune di ", "Comuna ", "Amphoe ", "Huyện ", "Općina ",
	"Kabupaten ", "Municipio ", "Powiat ", "Gemeente ", "Obshtina ", "Oraş ", "Municipiul ", "Okres ", "Markaz ", "Bashkia ", "Politischer Bezirk ", "Bezirk ",
	"Daïra ", "Srŏk ", "Distrito ", "Partido ", "Departamento ", "Provincia ",
}

// hyphenSuffixes are the Japanese endings written after a hyphen ("Fukushima-ken", "Kyōto-shi", "Aichi-gun").
var hyphenSuffixes = []string{"-ken", "-shi", "-gun"}

// CleanName drops the generic suffix words from the end of a place name ("Adıyaman Province" is "Adıyaman", "Üsküdar İlçesi" is "Üsküdar"), as many as
// there are ("Foo District Municipality" is "Foo"). A name that is nothing but a suffix word is kept.
func CleanName(name string) string {
	for {
		next := clean(name)
		if next == name {
			return name
		}
		name = next
	}
}

func clean(name string) string {
	name = strings.TrimSpace(name)
	for _, p := range prefixes {
		if len(name) > len(p) && strings.EqualFold(name[:len(p)], p) {
			name = strings.TrimSpace(name[len(p):])
			break
		}
	}
	for _, h := range hyphenSuffixes {
		if len(name) > len(h) && strings.EqualFold(name[len(name)-len(h):], h) {
			name = name[:len(name)-len(h)]
			break
		}
	}
	for {
		i := strings.LastIndexAny(name, " \u00a0")
		if i <= 0 {
			return name
		}
		if !isSuffix(name[i+1:]) {
			return name
		}
		name = strings.TrimSpace(name[:i])
	}
}

func isSuffix(word string) bool {
	for _, s := range suffixes {
		if strings.EqualFold(word, s) {
			return true
		}
	}
	return false
}

// isLatin is whether every letter of the text is in the Latin script: a name in Cyrillic, Arabic or Han is not used as the name of a place (the
// console searches and sorts by the Latin ASCII name, and its addresses are read in Latin script).
func isLatin(s string) bool {
	letters := 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			if !unicode.Is(unicode.Latin, r) {
				return false
			}
			letters++
		}
	}
	return letters > 0
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// altNames holds, per GeoNames id, the best alternate name of each language Build asked for.
type altNames map[int]map[string]string

// readAlternates reads alternateNamesV2.txt (alternateNameId, geonameid, isolanguage, alternate name, isPreferredName, isShortName, isColloquial,
// isHistoric, from, to) and keeps, for the ids in `want` and the languages in `langs`, one name per id and language: a preferred name first, else the
// first plain one. Short, colloquial and historic names are never taken.
func readAlternates(r io.Reader, want map[int]bool, langs map[string]bool) (altNames, error) {
	out := altNames{}
	preferred := map[[2]string]bool{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		f := strings.Split(sc.Text(), "\t")
		if len(f) < 8 || !langs[f[2]] || f[5] == "1" || f[6] == "1" || f[7] == "1" {
			continue
		}
		id := atoi(f[1])
		if !want[id] || f[3] == "" {
			continue
		}
		m := out[id]
		if m == nil {
			m = map[string]string{}
			out[id] = m
		}
		key := [2]string{f[1], f[2]}
		if _, have := m[f[2]]; have && (preferred[key] || f[4] != "1") {
			continue
		}
		m[f[2]] = f[3]
		preferred[key] = f[4] == "1"
	}
	return out, sc.Err()
}

// pick chooses the name a place is listed by: its name in the country's own language when that is written in Latin script, else the name GeoNames gives
// it when that is Latin ("Kadıköy İlçesi" is local already), else its English alternate name, else the GeoNames name as it is; then the generic words
// around it are dropped.
func (a altNames) pick(id int, lang, name string) string {
	if m := a[id]; m != nil {
		if n := m[lang]; n != "" && isLatin(n) {
			return CleanName(n)
		}
		if !isLatin(name) {
			if n := m["en"]; n != "" && isLatin(n) {
				return CleanName(n)
			}
		}
	}
	return CleanName(name)
}
