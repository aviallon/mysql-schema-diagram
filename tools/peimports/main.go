// Command peimports vérifie qu'un binaire Windows est autonome : il lit la
// table d'import de l'en-tête PE et échoue si le binaire importe une DLL
// *hôte*, c'est-à-dire une DLL qu'il faudrait embarquer à côté de l'exécutable.
//
// Pourquoi pas « importe une DLL » tout court : un binaire Windows produit par
// Go importe toujours kernel32.dll. Ce n'est pas une dépendance à embarquer,
// c'est le noyau Windows, présent sur toutes les machines, et c'est le runtime
// Go lui-même qui le déclare (runtime/os_windows.go, 51 directives
// //go:cgo_import_dynamic). Mesuré : un programme `package main; func main(){}`
// compilé avec CGO_ENABLED=0 GOOS=windows GOARCH=amd64 importe kernel32.dll, et
// aucune option de compilation ne le retire. « Aucune DLL importée » est donc
// impossible à obtenir pour un binaire Go ; la propriété réellement vérifiable,
// et celle qui compte, est « aucune DLL hôte ».
//
// La porte n'utilise que la bibliothèque standard (debug/pe) : pas de binutils,
// pas de chaîne croisée mingw, donc utilisable sur n'importe quel runner de CI.
// Elle remplace avantageusement un `objdump -p | grep -i "DLL Name"`, qui lit
// mal l'import table des binaires Go (il attribue .idata à la section de debug
// voisine et n'affiche alors aucune DLL : un faux succès).
//
// Usage :
//
//	peimports [-strict] [-allow dll[,dll...]] binaire1.exe [binaire2.exe ...]
//
// Sortie : 0 si aucun binaire n'importe de DLL hôte, 1 sinon, 2 en cas d'erreur
// d'usage ou de fichier illisible.
package main

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

const (
	// dirEntryImport est l'index IMAGE_DIRECTORY_ENTRY_IMPORT de la table des
	// répertoires de données de l'en-tête optionnel.
	dirEntryImport = 1
	// importDescSize est la taille d'un IMAGE_IMPORT_DESCRIPTOR.
	importDescSize = 20
	// maxNameLen borne la lecture d'un nom de DLL (chaîne terminée par NUL).
	maxNameLen = 512
	// maxDescriptors borne le parcours de la table d'import, pour qu'un fichier
	// corrompu ne fasse pas boucler l'outil.
	maxDescriptors = 4096
)

// DefaultAllowed liste les DLL considérées comme faisant partie du système :
// les importer ne rend pas le binaire dépendant d'un fichier à embarquer.
//
// Cette liste est volontairement minimale — une seule entrée, mesurée — pour
// que la porte reste stricte : msvcrt.dll, vcruntime140.dll, api-ms-win-crt-*
// ou libgcc_s_seh-1.dll signalent tous une compilation cgo/mingw, et sont donc
// refusés même s'ils existent sur un Windows donné.
var DefaultAllowed = []string{"kernel32.dll"}

// Report regroupe ce qu'un binaire PE importe.
type Report struct {
	// Machine est l'architecture cible ("amd64", "arm64", ...).
	Machine string
	// Imports est la liste triée et dédoublonnée de toutes les DLL importées.
	Imports []string
	// Host est le sous-ensemble de Imports à embarquer (hors systèmes autorisés).
	Host []string
	// Allowed est le sous-ensemble de Imports considéré comme système.
	Allowed []string
}

// SelfContained indique que le binaire n'importe aucune DLL hôte : il n'a
// besoin d'aucun fichier à côté de lui pour démarrer.
func (r Report) SelfContained() bool { return len(r.Host) == 0 }

// Analyze lit un binaire PE depuis r et renvoie la liste des DLL qu'il importe.
//
// allowed énumère les DLL traitées comme système (comparaison insensible à la
// casse) ; une liste vide rend la porte stricte.
func Analyze(r io.ReaderAt, allowed []string) (Report, error) {
	f, err := pe.NewFile(r)
	if err != nil {
		return Report{}, fmt.Errorf("en-tête PE illisible : %w", err)
	}
	defer f.Close()

	imports, err := importedDLLs(f)
	if err != nil {
		return Report{}, err
	}

	isAllowed := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		isAllowed[strings.ToLower(strings.TrimSpace(name))] = true
	}

	rep := Report{Machine: machineName(f.Machine), Imports: imports}
	for _, name := range imports {
		if isAllowed[strings.ToLower(name)] {
			rep.Allowed = append(rep.Allowed, name)
			continue
		}
		rep.Host = append(rep.Host, name)
	}
	return rep, nil
}

// analyzeFile analyse le fichier nommé.
func analyzeFile(path string, allowed []string) (Report, error) {
	f, err := os.Open(path)
	if err != nil {
		return Report{}, err
	}
	defer f.Close()
	rep, err := Analyze(f, allowed)
	if err != nil {
		return Report{}, fmt.Errorf("%s : %w", path, err)
	}
	return rep, nil
}

// importedDLLs parcourt IMAGE_IMPORT_DESCRIPTOR[] et renvoie les noms des DLL.
//
// debug/pe n'expose volontairement pas de fonction pour cela (son
// ImportedLibraries est un no-op), d'où ce parcours explicite.
func importedDLLs(f *pe.File) ([]string, error) {
	rva, size, ok := importDirectory(f)
	if !ok {
		// Aucun répertoire d'import : le binaire n'importe rien du tout.
		return nil, nil
	}

	r := rvaReader{f: f}
	seen := map[string]bool{}
	var out []string
	for i := 0; i < maxDescriptors; i++ {
		off := uint32(i * importDescSize)
		if size > 0 && off+importDescSize > size {
			break
		}
		desc, err := r.read(rva+off, importDescSize)
		if err != nil {
			return nil, fmt.Errorf("descripteur d'import %d : %w", i, err)
		}
		nameRVA := binary.LittleEndian.Uint32(desc[12:16])
		firstThunk := binary.LittleEndian.Uint32(desc[16:20])
		if nameRVA == 0 && firstThunk == 0 {
			// Descripteur nul de fin de table.
			break
		}
		if nameRVA == 0 {
			continue
		}
		name, err := r.cstring(nameRVA, maxNameLen)
		if err != nil {
			return nil, fmt.Errorf("nom de la DLL n° %d : %w", i, err)
		}
		if name == "" || seen[strings.ToLower(name)] {
			continue
		}
		seen[strings.ToLower(name)] = true
		out = append(out, name)
	}

	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out, nil
}

// importDirectory renvoie l'adresse et la taille du répertoire d'import.
func importDirectory(f *pe.File) (rva, size uint32, ok bool) {
	var dirs []pe.DataDirectory
	switch h := f.OptionalHeader.(type) {
	case *pe.OptionalHeader64:
		dirs = h.DataDirectory[:]
	case *pe.OptionalHeader32:
		dirs = h.DataDirectory[:]
	default:
		return 0, 0, false
	}
	if len(dirs) <= dirEntryImport {
		return 0, 0, false
	}
	d := dirs[dirEntryImport]
	if d.VirtualAddress == 0 {
		return 0, 0, false
	}
	return d.VirtualAddress, d.Size, true
}

// machineName traduit l'architecture PE en nom Go.
func machineName(machine uint16) string {
	switch machine {
	case pe.IMAGE_FILE_MACHINE_AMD64:
		return "amd64"
	case pe.IMAGE_FILE_MACHINE_ARM64:
		return "arm64"
	case pe.IMAGE_FILE_MACHINE_I386:
		return "386"
	case pe.IMAGE_FILE_MACHINE_ARMNT:
		return "arm"
	default:
		return fmt.Sprintf("0x%04x", machine)
	}
}

// rvaReader lit des octets à partir d'une adresse virtuelle relative (RVA), en
// passant par la section qui la contient.
type rvaReader struct {
	f *pe.File
}

// section renvoie la section couvrant rva, et l'offset de rva dans ses données.
func (r rvaReader) section(rva uint32) (*pe.Section, uint32, bool) {
	for _, s := range r.f.Sections {
		span := s.VirtualSize
		if s.Size > span {
			span = s.Size
		}
		if rva >= s.VirtualAddress && rva < s.VirtualAddress+span {
			return s, rva - s.VirtualAddress, true
		}
	}
	return nil, 0, false
}

// read renvoie n octets à partir de rva.
func (r rvaReader) read(rva uint32, n int) ([]byte, error) {
	s, off, ok := r.section(rva)
	if !ok {
		return nil, fmt.Errorf("RVA 0x%x hors de toute section", rva)
	}
	data, err := s.Data()
	if err != nil {
		return nil, err
	}
	if uint64(off)+uint64(n) > uint64(len(data)) {
		return nil, fmt.Errorf("RVA 0x%x (%d octets) dépasse la section %s", rva, n, s.Name)
	}
	return data[off : off+uint32(n)], nil
}

// cstring lit une chaîne terminée par NUL à partir de rva, au plus max octets.
func (r rvaReader) cstring(rva uint32, max int) (string, error) {
	s, off, ok := r.section(rva)
	if !ok {
		return "", fmt.Errorf("RVA 0x%x hors de toute section", rva)
	}
	data, err := s.Data()
	if err != nil {
		return "", err
	}
	if int(off) >= len(data) {
		return "", nil
	}
	end := len(data)
	if end > int(off)+max {
		end = int(off) + max
	}
	b := data[off:end]
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b), nil
}

// type listeDLL accepte un drapeau répétable et séparé par des virgules.
type listeDLL []string

func (l *listeDLL) String() string { return strings.Join(*l, ",") }

func (l *listeDLL) Set(v string) error {
	for _, item := range strings.Split(v, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			*l = append(*l, item)
		}
	}
	return nil
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run est la logique de l'outil, isolée pour être testable.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("peimports", flag.ContinueOnError)
	fs.SetOutput(stderr)
	strict := fs.Bool("strict", false, "refuser TOUTE DLL importée, kernel32.dll compris (un binaire Go en importe toujours : informatif)")
	var allowed listeDLL
	fs.Var(&allowed, "allow", "DLL à considérer comme système (répétable, séparée par des virgules)")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage : peimports [-strict] [-allow dll[,dll...]] <fichier.exe> [autre.exe ...]")
		fmt.Fprintln(stderr, "sortie : 0 si aucun binaire n'importe de DLL hôte, 1 sinon, 2 en erreur")
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	paths := fs.Args()
	if len(paths) == 0 {
		fs.Usage()
		return 2
	}

	switch {
	case *strict:
		allowed = nil
	case len(allowed) == 0:
		allowed = DefaultAllowed
	}

	failed := 0
	for _, path := range paths {
		rep, err := analyzeFile(path, allowed)
		if err != nil {
			fmt.Fprintf(stderr, "%-30s ERREUR : %v\n", path, err)
			return 2
		}
		switch {
		case rep.SelfContained() && len(rep.Imports) == 0:
			fmt.Fprintf(stdout, "%-30s machine %s — aucune DLL importée\n", path, rep.Machine)
		case rep.SelfContained():
			fmt.Fprintf(stdout, "%-30s machine %s — aucune DLL hôte (système, toujours présent : %s)\n",
				path, rep.Machine, strings.Join(rep.Allowed, ", "))
		default:
			failed++
			fmt.Fprintf(stdout, "%-30s machine %s — ÉCHEC, DLL hôte(s) à embarquer : %s",
				path, rep.Machine, strings.Join(rep.Host, ", "))
			if len(rep.Allowed) > 0 {
				fmt.Fprintf(stdout, " (système : %s)", strings.Join(rep.Allowed, ", "))
			}
			fmt.Fprintln(stdout)
		}
	}

	if failed > 0 {
		fmt.Fprintf(stderr, "porte de staticité : ÉCHEC — %d binaire(s) importe(nt) une DLL hôte\n", failed)
		return 1
	}
	return 0
}
