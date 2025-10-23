package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	// epochs maps a timestamp -> a row (field -> value) for that timestamp
	epochs map[string]map[string]any
)

// ----- small helpers -----

// ensure unique PRNs per system (returns indexes to keep, not a new PRN slice)
func uniqPRNIndexes(prns []int) []int {
	seen := make(map[int]struct{}, len(prns))
	keep := make([]int, 0, len(prns))
	for idx, p := range prns {
		if p <= 0 {
			continue
		}
		if _, ok := seen[p]; ok {
			continue // duplicate PRN found later in the array -> skip this index
		}
		seen[p] = struct{}{}
		keep = append(keep, idx)
	}
	// NOTE: do NOT sort indexes here unless you will also permute the
	// measurement arrays or are okay with breaking the VS alignment.
	return keep
}

func headerLine(body, label string) string {
	if len(body) > 60 {
		body = body[:60]
	}
	return fmt.Sprintf("%-60s%-20s\n", body, label)
}

func parseEpoch(s string) (time.Time, error) {
	const layout = "2006-01-02 15:04:05"
	return time.ParseInLocation(layout, strings.TrimSpace(s), time.UTC)
}

func sortedEpochKeys() []string {
	keys := make([]string, 0, len(epochs))
	for k := range epochs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func estimateInterval(sortedKeys []string) float64 {
	if len(sortedKeys) < 2 {
		return 1.0
	}
	t0, e0 := parseEpoch(sortedKeys[0])
	t1, e1 := parseEpoch(sortedKeys[1])
	if e0 != nil || e1 != nil {
		return 1.0
	}
	sec := t1.Sub(t0).Seconds()
	if sec <= 0 {
		return 1.0
	}
	return sec
}

// Format SYS/#/OBS TYPES lines (up to 13 types per line)
func formatSysObsTypesLines(sys rune, types []string) string {
	if len(types) == 0 {
		return ""
	}
	const maxPerLine = 13
	var sb strings.Builder

	writeChunk := func(start int, first bool) {
		end := start + maxPerLine
		if end > len(types) {
			end = len(types)
		}
		chunk := types[start:end]

		if first {
			body := fmt.Sprintf("%c%3d ", sys, len(types))
			for i, t := range chunk {
				if i == 0 {
					body += fmt.Sprintf("%4s", t)
				} else {
					body += fmt.Sprintf(" %4s", t)
				}
			}
			sb.WriteString(headerLine(body, "SYS / # / OBS TYPES"))
		} else {
			body := fmt.Sprintf("%c    ", sys)
			for i, t := range chunk {
				if i == 0 {
					body += fmt.Sprintf("%4s", t)
				} else {
					body += fmt.Sprintf(" %4s", t)
				}
			}
			sb.WriteString(headerLine(body, "SYS / # / OBS TYPES"))
		}
	}

	writeChunk(0, true)
	for i := maxPerLine; i < len(types); i += maxPerLine {
		writeChunk(i, false)
	}
	return sb.String()
}

// Build the exact observation type lists you requested.
// Order is C, L, D, S per band, and bands are the ones you specified.
func fixedSysObsTypes() map[rune][]string {
	return map[rune][]string{
		'G': {"C1C", "L1C", "D1C", "S1C", "C2C", "L2C", "D2C", "S2C"},
		'R': {"C1C", "L1C", "D1C", "S1C", "C2C", "L2C", "D2C", "S2C"},
		'E': {"C1X", "L1X", "D1X", "S1X", "C7X", "L7X", "D7X", "S7X"},
		// B is printed as C (BeiDou), with bands 2 and 7 using X attribute:
		'C': {"C2X", "L2X", "D2X", "S2X", "C7X", "L7X", "D7X", "S7X"},
		// Q is printed as J (QZSS), with 1 and 2 (L2X attribute by your sample):
		'J': {"C1C", "L1C", "D1C", "S1C", "C2X", "L2X", "D2X", "S2X"},
	}
}

// --- GLONASS SLOT / FRQ # section ---
// slots is a map: PRN -> frequency channel (e.g., 1:-7..+6)
// Lines carry up to 8 pairs per line, matching typical practice.
func formatGlonassSlotFreqLines(slots map[int]int) string {
	if len(slots) == 0 {
		return "" // nothing to print
	}
	type pair struct{ prn, chanN int }
	pairs := make([]pair, 0, len(slots))
	for prn, ch := range slots {
		pairs = append(pairs, pair{prn, ch})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].prn < pairs[j].prn })

	const pairsPerLine = 8
	var sb strings.Builder

	// First line starts with the count (right‑justified in 3 columns)
	writeLine := func(start int, first bool) {
		end := start + pairsPerLine
		if end > len(pairs) {
			end = len(pairs)
		}
		chunk := pairs[start:end]

		var body string
		if first {
			body = fmt.Sprintf("%3d", len(pairs))
		} else {
			body = "   " // 3 spaces align with first line's count field
		}
		// Append pairs: " Rnn  cc"
		for _, p := range chunk {
			body += fmt.Sprintf(" R%02d %2d", p.prn, p.chanN)
		}
		sb.WriteString(headerLine(body, "GLONASS SLOT / FRQ #"))
	}

	writeLine(0, true)
	for i := pairsPerLine; i < len(pairs); i += pairsPerLine {
		writeLine(i, false)
	}
	return sb.String()
}

// --- SYS / PHASE SHIFT section ---
// shifts: map[system]map[obsType]value, e.g. shifts['G']["L1C"]=0.0
// One header line per (system, obsType).
func formatSysPhaseShiftLines(shifts map[rune]map[string]float64) string {
	if len(shifts) == 0 {
		return ""
	}
	var sb strings.Builder
	// Deterministic order: system G,R,E,C,J then obs types sorted lexicographically
	sysOrder := []rune{'G', 'R', 'E', 'C', 'J'}
	for _, sys := range sysOrder {
		m, ok := shifts[sys]
		if !ok || len(m) == 0 {
			continue
		}
		types := make([]string, 0, len(m))
		for t := range m {
			types = append(types, t)
		}
		sort.Strings(types)
		for _, t := range types {
			val := m[t]
			body := fmt.Sprintf("%c %-3s %9.5f", sys, t, val)
			sb.WriteString(headerLine(body, "SYS / PHASE SHIFT"))
		}
	}
	return sb.String()
}

// The header using fixed SYS/OBS_TYPES and dynamic first/last and interval.
func getHeaderFixed() (string, error) {
	if len(epochs) == 0 {
		return "", fmt.Errorf("no epochs available")
	}
	keys := sortedEpochKeys()
	firstTS, err := parseEpoch(keys[0])
	if err != nil {
		return "", fmt.Errorf("parse first epoch %q: %w", keys[0], err)
	}
	lastTS, err := parseEpoch(keys[len(keys)-1])
	if err != nil {
		return "", fmt.Errorf("parse last epoch %q: %w", keys[len(keys)-1], err)
	}
	intervalSec := estimateInterval(keys)

	sysToTypes := fixedSysObsTypes()
	orderSys := []rune{'G', 'R', 'E', 'C', 'J'}

	var hdr strings.Builder
	hdr.WriteString(headerLine("     3.04           OBSERVATION DATA    M: MIXED", "RINEX VERSION / TYPE"))
	hdr.WriteString(headerLine(
		fmt.Sprintf("%-20s%-20s%-20s", "gnss-json-to-rinex3", "User", time.Now().UTC().Format("20060102 150405 UTC")),
		"PGM / RUN BY / DATE",
	))
	hdr.WriteString(headerLine("Generated automatically from JSON observations", "COMMENT"))

	for _, sys := range orderSys {
		if types := sysToTypes[sys]; len(types) > 0 {
			hdr.WriteString(formatSysObsTypesLines(sys, types))
		}
	}

	hdr.WriteString(headerLine("DBHZ", "SIGNAL STRENGTH UNIT"))
	hdr.WriteString(headerLine(fmt.Sprintf("%14.3f", intervalSec), "INTERVAL"))

	// TIME OF FIRST / LAST OBS (RINEX 3 lines)
	hdr.WriteString(headerLine(
		fmt.Sprintf("%6d%6d%6d%6d%6d%13.7f     GPS",
			firstTS.Year(), int(firstTS.Month()), firstTS.Day(),
			firstTS.Hour(), firstTS.Minute(), float64(firstTS.Second())),
		"TIME OF FIRST OBS",
	))
	hdr.WriteString(headerLine(
		fmt.Sprintf("%6d%6d%6d%6d%6d%13.7f     GPS",
			lastTS.Year(), int(lastTS.Month()), lastTS.Day(),
			lastTS.Hour(), lastTS.Minute(), float64(lastTS.Second())),
		"TIME OF LAST OBS",
	))

	hdr.WriteString(headerLine("0", "RCV CLOCK OFFS APPL"))
	hdr.WriteString(headerLine("", "END OF HEADER"))

	return hdr.String(), nil
}

func epanic(e error) {
	if e != nil {
		panic(e)
	}
}

// Output: "> 2025 10 21 15 42 07.0000000  0"
func formatObsEpoch(s string) string {
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", s, time.UTC); err == nil {
		return fmt.Sprintf("> %04d %02d %02d %02d %02d %02d.0000000  0",
			t.Year(), int(t.Month()), t.Day(), t.Hour(), t.Minute(), t.Second())
	} else {
		return s
	}
}

// Check if ./rinex exists, and tries to create it if not.
func ensureRinexDir() {
	const dir = "./rinex"
	info, err := os.Stat(dir)
	if err == nil {
		if info.IsDir() {
			return // already exists
		}
		// Exists but is not a directory
		fmt.Println("A file named ./rinex exists but is not a directory. Please fix it and try again.")
		os.Exit(1)
	} else if !os.IsNotExist(err) {
		// Some other I/O error when trying to stat
		fmt.Printf("Unable to access %s: %v\n", dir, err)
		os.Exit(1)
	}

	// Try to create it (permissions 0755 is typical for directories)
	if mkErr := os.Mkdir(dir, 0o755); mkErr != nil {
		fmt.Println("Unable to create the ./rinex/ directory. Please create it and try again.")
		os.Exit(1)
	}
}

func createEpochs(path string) {
	// Read the file using the full path
	data, err := os.ReadFile(path)
	epanic(err)

	// Generic container: map[string]any
	var content map[string]any
	if err := json.Unmarshal(data, &content); err != nil {
		epanic(fmt.Errorf("JSON unmarshal failed for %s: %w", path, err))
	}

	// Extract recordTime as []string
	rawRT, ok := content["recordTime"]
	if !ok {
		epanic(fmt.Errorf("field %q not found in %s", "recordTime", path))
	}
	rtSlice, ok := rawRT.([]any)
	if !ok {
		epanic(fmt.Errorf("recordTime has type %T; expected array", rawRT))
	}

	recordTimes := make([]string, len(rtSlice))
	for i, v := range rtSlice {
		s, ok := v.(string)
		if !ok {
			epanic(fmt.Errorf("recordTime[%d] has type %T; expected string", i, v))
		}
		recordTimes[i] = s
	}

	numEpochs := len(recordTimes)
	epochs = make(map[string]map[string]any, numEpochs)

	// For every epoch index, grab the i-th element from each other field (if present)
	for i := range numEpochs {
		ts := recordTimes[i]
		row := make(map[string]any)

		for key, val := range content {

			if key == "recordTime" {
				// // Keep the timestamp in the row as well
				// row[key] = recordTimes[i]
				continue
			}

			// Most fields are arrays (one element per epoch).
			// They often are [][]<something>, which unmarshal as []any (outer) of []any (inner).
			outer, ok := val.([]any)
			if !ok {
				// // If a field is not an array, we can still carry it through as-is (constant across epochs).
				// row[key] = val
				continue
			}

			if i < len(outer) {
				// The per-epoch value could be []any (e.g., a vector), or any other JSON type.
				row[key] = outer[i]
			} else {
				// We’ll set an empty slice to mirror the structure.
				row[key] = []any{}
			}
		}

		epochs[ts] = row
	}
}

// Safe type helpers
func asSliceAny(v any) ([]any, bool) { vv, ok := v.([]any); return vv, ok }

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	default:
		return 0, false
	}
}
func toInt(v any) (int, bool) {
	f, ok := toFloat(v)
	if !ok {
		return 0, false
	}
	return int(math.Round(f)), true
}

func fmtObs(val any, prec int) string {
	if f, ok := toFloat(val); ok {
		return fmt.Sprintf("%14.*f", prec, f)
	}
	return fmt.Sprintf("%14s", "")
}
func precFor(prefix string) int {
	switch prefix {
	case "cpMes_":
		return 5 // phase
	case "prMes_":
		return 3 // code
	case "doMes_":
		return 3 // Doppler
	case "cn0_":
		return 3 // SNR
	default:
		return 3
	}
}
func satID(sys rune, prn int) string {
	return fmt.Sprintf("%c%02d", sys, prn)
}

// Mapping: display system -> data system rune used in JSON suffixes
// G->G, R->R, E->E, C->B, J->Q (B is actually C; Q is actually J)
var displayToDataSys = map[rune]rune{
	'G': 'G',
	'R': 'R',
	'E': 'E',
	'C': 'B',
	'J': 'Q',
}

// VS keys by display sys (mapped to underlying data sys)
func vsKeyFor(displaySys rune) string {
	switch displaySys {
	case 'G':
		return "VSG"
	case 'R':
		return "VSR"
	case 'E':
		return "VSE"
	case 'C': // BeiDou data is under B
		return "VSB"
	case 'J': // QZSS data is under Q
		return "VSQ"
	default:
		return ""
	}
}

// Bands per display system (printed order), and corresponding data suffix rune
func bandsFor(displaySys rune) []string {
	switch displaySys {
	case 'G':
		return []string{"1", "2"}
	case 'R':
		return []string{"1", "2"}
	case 'E':
		return []string{"1", "7"}
	case 'C': // BeiDou printed as C, but data is B2/B7
		return []string{"2", "7"}
	case 'J': // QZSS printed as J, data is Q1/Q2
		return []string{"1", "2"}
	default:
		return nil
	}
}

// Observation order per band (C,L,D,S)
var measPrefixes = []string{"prMes_", "cpMes_", "doMes_", "cn0_"}

// Skip rows where all VS (G,E,B,Q,R) at index j are zero
func getVSVal(content map[string]any, key string, j int) float64 {
	raw, ok := content[key]
	if !ok {
		return 0
	}
	slice, ok := asSliceAny(raw)
	if !ok || j < 0 || j >= len(slice) {
		return 0
	}
	if f, ok := toFloat(slice[j]); ok {
		return f
	}
	return 0
}
func isAllVSZero(content map[string]any, j int) bool {
	for _, k := range []string{"VSG", "VSE", "VSQ", "VSR", "VSB"} {
		if getVSVal(content, k, j) != 0 {
			return false
		}
	}
	return true
}

func writeBody(w io.Writer) error {
	// 1) Sort epoch keys
	keys := make([]string, 0, len(epochs))
	for ts := range epochs {
		keys = append(keys, ts)
	}
	sort.Strings(keys)

	// Constellation print order
	sysOrder := []rune{'G', 'R', 'E', 'C', 'J'}

	bw, _ := w.(*bufio.Writer)

	for _, ts := range keys {
		content := epochs[ts]

		// 2) Build PRN slices per system from the VS arrays (do NOT sort)
		sysPRNs := map[rune][]int{}
		for _, dispSys := range sysOrder {
			vsKey := vsKeyFor(dispSys) // e.g. "VSG", "VSR", "VSE", "VSB", "VSQ"
			vsVal, ok := content[vsKey]
			if !ok {
				continue
			}
			vsSlice, ok := asSliceAny(vsVal)
			if !ok {
				continue
			}

			prns := make([]int, len(vsSlice))
			for j, v := range vsSlice {
				if prn, ok := toInt(v); ok {
					prns[j] = prn
				}
			}
			sysPRNs[dispSys] = prns
		}

		// 3) Build the index lists to print (unique PRNs and not all-VS-zero)
		sysIdxToPrint := map[rune][]int{}
		totalPrinted := 0
		for _, dispSys := range sysOrder {
			prns := sysPRNs[dispSys]
			if len(prns) == 0 {
				continue
			}

			// unique PRNs -> get the indexes we will keep
			uniqIdx := uniqPRNIndexes(prns)

			// apply your “skip rows with all VS=0” rule per kept index
			kept := make([]int, 0, len(uniqIdx))
			for _, j := range uniqIdx {
				if prns[j] <= 0 {
					continue
				}
				if isAllVSZero(content, j) {
					continue
				}
				kept = append(kept, j)
			}

			sysIdxToPrint[dispSys] = kept
			totalPrinted += len(kept)
		}

		// 4) Epoch line
		epochLine := fmt.Sprintf("%s %d", formatObsEpoch(ts), totalPrinted)
		if _, err := fmt.Fprintln(w, epochLine); err != nil {
			return err
		}

		// 5) Emit rows in G,R,E,C,J order, using the kept indexes (alignment preserved)
		for _, dispSys := range sysOrder {
			prns := sysPRNs[dispSys]
			idxs := sysIdxToPrint[dispSys]
			if len(idxs) == 0 {
				continue
			}

			dataSys := displayToDataSys[dispSys] // C->B, J->Q mapping for suffixes
			bands := bandsFor(dispSys)           // e.g. G: ["1","2"], E: ["1","7"], C: ["2","7"] ...

			for _, j := range idxs {
				prn := prns[j]
				var b strings.Builder
				b.WriteString(satID(dispSys, prn)) // display letter
				// print CLDS per band
				for _, band := range bands {
					sfx := fmt.Sprintf("%c%s", dataSys, band) // data suffix (B or Q if C/J)
					for _, pref := range measPrefixes {       // "prMes_","cpMes_","doMes_","cn0_"
						key := pref + sfx
						val, has := content[key]
						if !has {
							b.WriteString(fmtObs(nil, precFor(pref)))
							continue
						}
						row, ok := asSliceAny(val)
						if !ok || j >= len(row) {
							b.WriteString(fmtObs(nil, precFor(pref)))
							continue
						}
						b.WriteString(fmtObs(row[j], precFor(pref)))
					}
				}

				if _, err := fmt.Fprintln(w, b.String()); err != nil {
					return err
				}
			}
		}

		if bw != nil {
			_ = bw.Flush()
		}
	}
	return nil
}

func main() {
	if len(os.Args) != 2 {
		fmt.Println("Please specify an observation file:")
		fmt.Printf("Example: %s %s\n", filepath.Base(os.Args[0]), "observation12.json")
		os.Exit(1)
	}

	// Create ./rinex/ dir if not present
	ensureRinexDir()

	file := os.Args[1]
	createEpochs(file)

	_, relativeFile := filepath.Split(file)
	fileName := strings.Split(relativeFile, ".")[0]

	header, err := getHeaderFixed()
	epanic(err)

	//  write to a file
	fileString := fmt.Sprintf("./rinex/%v.obs", fileName)
	f, err := os.Create(fileString)
	if err != nil {
		panic(err)
	}
	defer f.Close()

	bw := bufio.NewWriter(f)
	defer bw.Flush()

	if _, err := fmt.Fprint(bw, header); err != nil {
		epanic(err)
	}
	if err := writeBody(bw); err != nil {
		panic(err)
	}

}
