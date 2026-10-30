package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Les tests de ce fichier portent sur la porte elle-même : ils vérifient
// qu'elle ÉCHOUE sur un PE qui importe une DLL hôte. Une porte qui ne peut pas
// échouer ne prouve rien.

const (
	peHeaderOff = 0x40  // e_lfanew : signature PE
	optHdrSize  = 240   // taille de l'en-tête optionnel PE32+
	fileAlign   = 0x200 // alignement des données brutes
	sectionRVA  = 0x1000
	rawDataOff  = 0x200
)

// syntheticPE construit en mémoire un PE32+ minimal, mais bien formé.
//
// Si withImportDir est vrai, l'en-tête déclare un répertoire d'import contenant
// un IMAGE_IMPORT_DESCRIPTOR par DLL nommée, suivi du descripteur nul de fin.
// Sinon, la table des répertoires de données reste entièrement nulle : c'est le
// cas d'un binaire qui n'importe rien.
func syntheticPE(t *testing.T, withImportDir bool, dlls []string) []byte {
	t.Helper()

	descBytes := len(dlls)*importDescSize + importDescSize // + descripteur nul
	nameBase := uint32(sectionRVA + descBytes)

	var names bytes.Buffer
	nameRVA := make([]uint32, len(dlls))
	for i, dll := range dlls {
		nameRVA[i] = nameBase + uint32(names.Len())
		names.WriteString(dll)
		names.WriteByte(0)
	}

	body := make([]byte, 0, descBytes+names.Len())
	body = append(body, make([]byte, descBytes)...)
	body = append(body, names.Bytes()...)
	for i := range dlls {
		desc := body[i*importDescSize : (i+1)*importDescSize]
		binary.LittleEndian.PutUint32(desc[12:16], nameRVA[i]) // Name
	}
	// Le dernier descripteur (tout à zéro) termine la table.

	rawSize := (len(body) + fileAlign - 1) / fileAlign * fileAlign
	file := make([]byte, rawDataOff+rawSize)

	// En-tête DOS : « MZ » puis l'offset de la signature PE.
	file[0], file[1] = 'M', 'Z'
	binary.LittleEndian.PutUint32(file[0x3c:0x40], peHeaderOff)

	// Signature PE.
	copy(file[peHeaderOff:], "PE\x00\x00")

	// En-tête COFF.
	coff := file[peHeaderOff+4 : peHeaderOff+24]
	binary.LittleEndian.PutUint16(coff[0:2], 0x8664) // IMAGE_FILE_MACHINE_AMD64
	binary.LittleEndian.PutUint16(coff[2:4], 1)      // une section
	binary.LittleEndian.PutUint16(coff[16:18], optHdrSize)
	binary.LittleEndian.PutUint16(coff[18:20], 0x0022) // EXECUTABLE_IMAGE | LARGE_ADDRESS_AWARE

	// En-tête optionnel PE32+.
	opt := file[peHeaderOff+24 : peHeaderOff+24+optHdrSize]
	binary.LittleEndian.PutUint16(opt[0:2], 0x20b) // PE32+
	binary.LittleEndian.PutUint32(opt[32:36], 0x1000)
	binary.LittleEndian.PutUint32(opt[36:40], fileAlign)
	binary.LittleEndian.PutUint32(opt[56:60], 0x2000)     // SizeOfImage
	binary.LittleEndian.PutUint32(opt[60:64], rawDataOff) // SizeOfHeaders
	binary.LittleEndian.PutUint16(opt[68:70], 3)          // IMAGE_SUBSYSTEM_WINDOWS_CUI
	binary.LittleEndian.PutUint32(opt[108:112], 16)       // NumberOfRvaAndSizes
	if withImportDir {
		dd := opt[112+dirEntryImport*8 : 112+dirEntryImport*8+8]
		binary.LittleEndian.PutUint32(dd[0:4], sectionRVA)
		binary.LittleEndian.PutUint32(dd[4:8], uint32(descBytes))
	}

	// En-tête de section.
	sect := file[peHeaderOff+4+20+optHdrSize : peHeaderOff+4+20+optHdrSize+40]
	copy(sect[0:8], ".rdata")
	binary.LittleEndian.PutUint32(sect[8:12], uint32(len(body))) // VirtualSize
	binary.LittleEndian.PutUint32(sect[12:16], sectionRVA)
	binary.LittleEndian.PutUint32(sect[16:20], uint32(rawSize)) // SizeOfRawData
	binary.LittleEndian.PutUint32(sect[20:24], rawDataOff)
	binary.LittleEndian.PutUint32(sect[36:40], 0x40000040) // INITIALIZED_DATA | READ

	copy(file[rawDataOff:], body)
	return file
}

func writeTempPE(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "binaire.exe")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("écriture du PE de test : %v", err)
	}
	return path
}

func analyze(t *testing.T, pe []byte, allowed []string) Report {
	t.Helper()
	rep, err := Analyze(bytes.NewReader(pe), allowed)
	if err != nil {
		t.Fatalf("Analyze : %v", err)
	}
	return rep
}

// TestAnalyzeDetectsImportedDLL est le test central : sur un PE qui importe une
// DLL hôte, l'analyse doit la voir.
func TestAnalyzeDetectsImportedDLL(t *testing.T) {
	pe := syntheticPE(t, true, []string{"KERNEL32.dll", "VCRUNTIME140.dll"})

	rep := analyze(t, pe, DefaultAllowed)
	if rep.SelfContained() {
		t.Fatal("un PE important VCRUNTIME140.dll a été déclaré autonome")
	}
	if len(rep.Imports) != 2 || rep.Imports[0] != "KERNEL32.dll" || rep.Imports[1] != "VCRUNTIME140.dll" {
		t.Fatalf("imports = %v, attendu les deux DLL", rep.Imports)
	}
	if len(rep.Host) != 1 || rep.Host[0] != "VCRUNTIME140.dll" {
		t.Errorf("hôte = %v, attendu [VCRUNTIME140.dll]", rep.Host)
	}
	if len(rep.Allowed) != 1 || rep.Allowed[0] != "KERNEL32.dll" {
		t.Errorf("système = %v, attendu [KERNEL32.dll]", rep.Allowed)
	}
	if rep.Machine != "amd64" {
		t.Errorf("Machine = %q, attendu amd64", rep.Machine)
	}
}

// TestAnalyzeStaticPE vérifie le sens inverse : un PE qui n'importe rien est
// autonome, que le répertoire d'import existe ou non.
func TestAnalyzeStaticPE(t *testing.T) {
	cas := map[string][]byte{
		"sans répertoire d'import": syntheticPE(t, false, nil),
		"répertoire vide":          syntheticPE(t, true, nil),
	}
	for nom, pe := range cas {
		t.Run(nom, func(t *testing.T) {
			rep := analyze(t, pe, DefaultAllowed)
			if !rep.SelfContained() {
				t.Fatalf("imports inattendus : %v", rep.Imports)
			}
			if len(rep.Imports) != 0 {
				t.Fatalf("imports = %v, attendu vide", rep.Imports)
			}
		})
	}
}

// TestStrictModeRefusesKernel32 encode la mesure de départ : un binaire Go
// importe toujours kernel32.dll, donc la porte stricte le refuse, alors que la
// porte par défaut l'accepte (dépendance au noyau Windows, pas un fichier à
// embarquer). Si un jour le runtime Go cesse d'importer kernel32.dll, ce test
// continuera de passer — il porte sur la politique, pas sur le runtime.
func TestStrictModeRefusesKernel32(t *testing.T) {
	pe := syntheticPE(t, true, []string{"kernel32.dll"})

	defaut := analyze(t, pe, DefaultAllowed)
	if !defaut.SelfContained() {
		t.Errorf("politique par défaut : kernel32.dll ne devrait pas être une DLL hôte (%v)", defaut.Host)
	}

	stricte := analyze(t, pe, nil)
	if stricte.SelfContained() {
		t.Error("politique stricte : kernel32.dll devrait être refusée")
	}
	if len(stricte.Imports) != 1 {
		t.Errorf("la liste complète des imports reste lisible : %v", stricte.Imports)
	}
}

// TestAnalyzeDeduplicates vérifie que deux descripteurs nommant la même DLL ne
// produisent qu'une entrée (les DLL Windows sont insensibles à la casse).
func TestAnalyzeDeduplicates(t *testing.T) {
	pe := syntheticPE(t, true, []string{"USER32.dll", "user32.DLL"})
	rep := analyze(t, pe, DefaultAllowed)
	if len(rep.Imports) != 1 {
		t.Fatalf("imports = %v, attendu une seule entrée", rep.Imports)
	}
}

// TestAnalyzeRejectsNonPE vérifie que l'outil ne prend pas un fichier quelconque
// pour un binaire autonome.
func TestAnalyzeRejectsNonPE(t *testing.T) {
	if _, err := Analyze(strings.NewReader("ceci n'est pas un PE du tout"), DefaultAllowed); err == nil {
		t.Fatal("Analyze a accepté un fichier qui n'est pas un PE")
	}
}

// TestRunExitCodes vérifie la porte telle que la CI l'appelle : le code de
// sortie doit être 1 dès qu'une DLL hôte est importée, 0 sinon.
func TestRunExitCodes(t *testing.T) {
	vierge := writeTempPE(t, syntheticPE(t, true, nil))
	noyau := writeTempPE(t, syntheticPE(t, true, []string{"kernel32.dll"}))
	cgo := writeTempPE(t, syntheticPE(t, true, []string{"VCRUNTIME140.dll", "api-ms-win-crt-runtime-l1-1-0.dll"}))
	mingw := writeTempPE(t, syntheticPE(t, true, []string{"libgcc_s_seh-1.dll"}))

	t.Run("aucun import -> 0", func(t *testing.T) {
		var out, errOut bytes.Buffer
		if code := run([]string{vierge}, &out, &errOut); code != 0 {
			t.Fatalf("code = %d, attendu 0 (stdout=%q stderr=%q)", code, out.String(), errOut.String())
		}
		if !strings.Contains(out.String(), "aucune DLL importée") {
			t.Errorf("stdout ne mentionne pas la staticité : %q", out.String())
		}
	})

	t.Run("kernel32 seul -> 0", func(t *testing.T) {
		var out, errOut bytes.Buffer
		if code := run([]string{noyau}, &out, &errOut); code != 0 {
			t.Fatalf("code = %d, attendu 0 (stdout=%q stderr=%q)", code, out.String(), errOut.String())
		}
		if !strings.Contains(out.String(), "aucune DLL hôte") {
			t.Errorf("stdout ne dit pas que le binaire est autonome : %q", out.String())
		}
	})

	t.Run("kernel32 seul, -strict -> 1", func(t *testing.T) {
		var out, errOut bytes.Buffer
		if code := run([]string{"-strict", noyau}, &out, &errOut); code != 1 {
			t.Fatalf("code = %d, attendu 1 (stdout=%q)", code, out.String())
		}
	})

	t.Run("DLL cgo importée -> 1", func(t *testing.T) {
		var out, errOut bytes.Buffer
		if code := run([]string{cgo}, &out, &errOut); code != 1 {
			t.Fatalf("code = %d, attendu 1 (stdout=%q)", code, out.String())
		}
		if !strings.Contains(out.String(), "VCRUNTIME140.dll") {
			t.Errorf("stdout ne nomme pas la DLL importée : %q", out.String())
		}
	})

	t.Run("DLL mingw importée -> 1", func(t *testing.T) {
		var out, errOut bytes.Buffer
		if code := run([]string{mingw}, &out, &errOut); code != 1 {
			t.Fatalf("code = %d, attendu 1", code)
		}
	})

	t.Run("mélange sain puis fautif -> 1", func(t *testing.T) {
		var out, errOut bytes.Buffer
		if code := run([]string{vierge, noyau, cgo}, &out, &errOut); code != 1 {
			t.Fatalf("code = %d, attendu 1", code)
		}
		if strings.Contains(errOut.String(), "0 binaire") {
			t.Errorf("le compte d'échecs ne doit pas être nul : %q", errOut.String())
		}
	})

	t.Run("-allow personnalisé -> 0", func(t *testing.T) {
		var out, errOut bytes.Buffer
		if code := run([]string{"-allow", "libgcc_s_seh-1.DLL", mingw}, &out, &errOut); code != 0 {
			t.Fatalf("code = %d, attendu 0 (stdout=%q)", code, out.String())
		}
	})

	t.Run("aucun argument -> 2", func(t *testing.T) {
		var out, errOut bytes.Buffer
		if code := run(nil, &out, &errOut); code != 2 {
			t.Fatalf("code = %d, attendu 2", code)
		}
	})

	t.Run("fichier absent -> 2", func(t *testing.T) {
		var out, errOut bytes.Buffer
		if code := run([]string{filepath.Join(t.TempDir(), "absent.exe")}, &out, &errOut); code != 2 {
			t.Fatalf("code = %d, attendu 2", code)
		}
	})
}

// TestRealWindowsBinaries applique la porte aux binaires de dist/ quand ils
// sont présents : c'est la preuve que la propriété vaut aussi pour les vrais
// binaires, pas seulement pour une fixture synthétique.
//
// Le test encode la mesure : un binaire Go importe toujours kernel32.dll (le
// runtime Go la déclare), et rien d'autre. Si Go se met à importer une autre
// DLL système, ce test échoue et la liste DefaultAllowed doit être revue.
func TestRealWindowsBinaries(t *testing.T) {
	exes, err := filepath.Glob(filepath.Join("..", "..", "dist", "*.exe"))
	if err != nil {
		t.Fatalf("glob : %v", err)
	}
	if len(exes) == 0 {
		t.Skip("dist/ ne contient aucun .exe : lancer « make cross » d'abord")
	}
	for _, exe := range exes {
		t.Run(filepath.Base(exe), func(t *testing.T) {
			rep, err := Analyze(openFile(t, exe), DefaultAllowed)
			if err != nil {
				t.Fatalf("Analyze(%s) : %v", exe, err)
			}
			if !rep.SelfContained() {
				t.Errorf("%s importe des DLL hôtes : %v", exe, rep.Host)
			}
			if len(rep.Imports) == 0 {
				t.Errorf("%s n'importe rien du tout : suspect pour un binaire Go", exe)
			}
			for _, dll := range rep.Allowed {
				t.Logf("%s : %s (système, ignorée)", exe, dll)
			}

			// La porte doit refuser ces binaires en mode strict : c'est ce qui
			// prouve qu'elle n'est pas complaisante.
			if stricte, err := Analyze(openFile(t, exe), nil); err != nil || stricte.SelfContained() {
				t.Errorf("-strict devrait refuser %s (imports = %v, err = %v)", exe, stricte.Imports, err)
			}
		})
	}
}

func openFile(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("ouverture de %s : %v", path, err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}
