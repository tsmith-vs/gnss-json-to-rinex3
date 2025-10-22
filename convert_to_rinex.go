package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
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

// ---------- helpers ----------

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

func obsCodeForPrefix(measPrefix string) (string, bool) {
	switch measPrefix {
	case "prMes_":
		return "C", true // Code (pseudorange)
	case "cpMes_":
		return "L", true // Phase
	case "doMes_":
		return "D", true // Doppler
	case "cn0_":
		return "S", true // SNR (dB-Hz)
	default:
		return "", false
	}
}

// Attribute (3rd char) defaults; tweak to your dataset’s actual signal tags as needed
func attrFor(system rune, freq string) string {
	switch system {
	case 'G':
		// L1/L2 → C, L5 → X by default
		if strings.HasPrefix(freq, "5") {
			return "X"
		}
		return "C"
	case 'E':
		// Galileo often X for combined pilot+data
		return "X"
	case 'R':
		// GLONASS CA
		return "C"
	case 'B', 'Q':
		// BeiDou/QZSS legacy signals frequently I
		return "I"
	default:
		return "C"
	}
}

// Build dynamic sys->[]obsTypes from a single epoch (usually the first). If you want
// to union across *all* epochs, iterate all and accumulate instead of only first.
func buildSysObsTypes(firstContent map[string]any) map[rune][]string {
	type bandInfo struct {
		band string
		meas map[string]bool // letters: C/L/D/S present on this band
	}
	sysBands := map[rune]map[string]*bandInfo{}

	add := func(sys rune, band, letter string) {
		if sysBands[sys] == nil {
			sysBands[sys] = map[string]*bandInfo{}
		}
		if sysBands[sys][band] == nil {
			sysBands[sys][band] = &bandInfo{band: band, meas: map[string]bool{}}
		}
		sysBands[sys][band].meas[letter] = true
	}

	for key := range firstContent {
		idx := strings.Index(key, "_")
		if idx <= 0 || idx+1 >= len(key) {
			continue
		}
		prefix := key[:idx+1] // "prMes_", etc.
		sfx := key[idx+1:]    // "G1", "R1", "E5", "B1", "Q2", ...
		if len(sfx) < 2 {
			continue
		}
		sys := rune(sfx[0])
		freq := sfx[1:] // "1","2","5","5X",...

		letter, ok := obsCodeForPrefix(prefix)
		if !ok {
			continue
		}
		add(sys, freq, letter)
	}

	// Order within a band; CHANGE HERE if your body writes in a different order:
	orderMeas := []string{"C", "L", "D", "S"} // canonical RINEX order

	sysToTypes := map[rune][]string{}
	for sys, bands := range sysBands {
		// sort bands (lexicographically works for e.g. "1","2","5","5X")
		bandKeys := make([]string, 0, len(bands))
		for b := range bands {
			bandKeys = append(bandKeys, b)
		}
		sort.Strings(bandKeys)

		var types []string
		for _, b := range bandKeys {
			attr := attrFor(sys, b)
			for _, m := range orderMeas {
				if bands[b].meas[m] {
					types = append(types, m+b+attr) // e.g. C1C, L1C...
				}
			}
		}
		sysToTypes[sys] = types
	}
	return sysToTypes
}

// Format SYS/#/OBS TYPES lines, splitting every 13 types per line
func formatSysObsTypesLines(sys rune, types []string) string {
	if len(types) == 0 {
		return ""
	}
	const maxPerLine = 13
	var sb strings.Builder

	writeChunk := func(start, firstIdx int, first bool) {
		end := start + maxPerLine
		if end > len(types) {
			end = len(types)
		}
		chunk := types[start:end]

		if first {
			// sys in col1, count in cols 4-6 (3-wide, right‑justified)
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
			// continuation line: sys in col1, then 4 blanks
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

	// first line
	writeChunk(0, 0, true)
	// continuations
	for i := maxPerLine; i < len(types); i += maxPerLine {
		writeChunk(i, i, false)
	}
	return sb.String()
}

// ---------- main header builder ----------

func getHeaderDynamic() (string, error) {
	if len(epochs) == 0 {
		return "", fmt.Errorf("no epochs available to build header")
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

	// Build observation type sets from first epoch (or union over all, if you prefer)
	sysToTypes := buildSysObsTypes(epochs[keys[0]])

	orderSys := []rune{'G', 'R', 'E', 'B', 'Q'}

	var hdr strings.Builder
	// RINEX VERSION / TYPE
	hdr.WriteString(headerLine("3.04           OBSERVATION DATA    M: MIXED", "RINEX VERSION / TYPE"))
	// PGM / RUN BY / DATE
	hdr.WriteString(headerLine(
		fmt.Sprintf("%-20s%-20s%-20s", "gnss-json-to-rinex3", "User", time.Now().UTC().Format("20060102 150405 UTC")),
		"PGM / RUN BY / DATE",
	))
	// Optional COMMENT
	hdr.WriteString(headerLine("Generated automatically from JSON observations", "COMMENT"))

	// Dynamic SYS/#/OBS TYPES
	for _, sys := range orderSys {
		if types := sysToTypes[sys]; len(types) > 0 {
			hdr.WriteString(formatSysObsTypesLines(sys, types))
		}
	}

	// SIGNAL STRENGTH UNIT
	hdr.WriteString(headerLine("DBHZ", "SIGNAL STRENGTH UNIT"))
	// INTERVAL
	hdr.WriteString(headerLine(fmt.Sprintf("%14.3f", intervalSec), "INTERVAL"))

	// TIME OF FIRST / LAST OBS (GPS time system tag is acceptable here if using GPS time)
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

	// RCV CLOCK OFFS APPL
	hdr.WriteString(headerLine("0", "RCV CLOCK OFFS APPL"))
	// END OF HEADER
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
// toFloat as used earlier
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

func asSliceAny(v any) ([]any, bool) { vv, ok := v.([]any); return vv, ok }

// getVSVal returns the numeric value at index j for a given VS key (e.g., "VSG").
// Missing key, non-slice, or out-of-bounds -> treated as 0.0.
func getVSVal(content map[string]any, key string, j int) float64 {
	raw, ok := content[key]
	if !ok {
		return 0
	}
	slice, ok := asSliceAny(raw)
	if !ok || j >= len(slice) || j < 0 {
		return 0
	}
	if f, ok := toFloat(slice[j]); ok {
		return f
	}
	return 0
}

// isAllVSZero returns true if VSG, VSE, VSQ, VSR, and VSB are all zero at index j.
func isAllVSZero(content map[string]any, j int) bool {
	vsKeys := []string{"VSG", "VSE", "VSQ", "VSR", "VSB"}
	for _, k := range vsKeys {
		if getVSVal(content, k, j) != 0 {
			return false
		}
	}
	return true
}
func toInt(v any) (int, bool) {
	f, ok := toFloat(v)
	if !ok {
		return 0, false
	}
	return int(f + 0.5), true // round
}

// width=14 fields similar to your example, pick precision per type
func fmtObs(val any, prec int) string {
	if f, ok := toFloat(val); ok {
		return fmt.Sprintf("%14.*f", prec, f)
	}
	// missing value → blank field
	return fmt.Sprintf("%14s", "")
}

// precision per measurement type
func precFor(field string) int {
	switch {
	case strings.HasPrefix(field, "cpMes"): // carrier phase
		return 5
	case strings.HasPrefix(field, "prMes"): // pseudorange
		return 3
	case strings.HasPrefix(field, "doMes"): // Doppler
		return 3
	case strings.HasPrefix(field, "cn0"): // SNR (dB-Hz)
		return 3
	default:
		return 3
	}
}

// Build satellite ID like "G06", "R01", etc.
func satID(sys rune, prn int) string {
	return fmt.Sprintf("%c%02d", sys, prn)
}

// writeBody writes the entire observation body to w using the global `epochs`.
func writeBody(w io.Writer) error {
	// --- sort epoch keys
	keys := make([]string, 0, len(epochs))
	for ts := range epochs {
		keys = append(keys, ts)
	}
	sort.Strings(keys)

	for _, ts := range keys {
		content := epochs[ts] // map[string]any (per-epoch)

		// discover system->VS key (e.g., 'G'->"VSG")
		sysToVS := map[rune]string{}
		for k := range content {
			if len(k) == 3 && strings.HasPrefix(k, "VS") {
				sysToVS[rune(k[2])] = k
			}
		}

		// build PRN lists per system using VS*
		sysPRNs := map[rune][]int{}
		for sys, vsKey := range sysToVS {
			vsVal, ok := content[vsKey]
			if !ok {
				continue
			}
			vsSlice, ok := asSliceAny(vsVal)
			if !ok {
				continue
			}
			prns := make([]int, 0, len(vsSlice))
			for _, v := range vsSlice {
				if prn, ok := toInt(v); ok && prn > 0 {
					prns = append(prns, prn)
				} else {
					// keep slot for alignment even if PRN not positive
					prns = append(prns, 0)
				}
			}
			sysPRNs[sys] = prns
		}

		// compute total *printable* satellites only after we apply the skip rule
		// but we need the epoch line's count beforehand. Easiest is:
		//  1) count printable first,
		//  2) then print header with that count,
		//  3) then print rows.

		// discover bands per system (e.g., ["G1","G2"])
		sysBands := map[rune][]string{}
		for k := range content {
			if idx := strings.Index(k, "_"); idx >= 0 && idx+1 < len(k) {
				sfx := k[idx+1:]
				if len(sfx) >= 2 {
					sys := rune(sfx[0])
					if strings.HasPrefix(k, "prMes_") || strings.HasPrefix(k, "cpMes_") ||
						strings.HasPrefix(k, "doMes_") || strings.HasPrefix(k, "cn0_") {
						found := false
						for _, b := range sysBands[sys] {
							if b == sfx {
								found = true
								break
							}
						}
						if !found {
							sysBands[sys] = append(sysBands[sys], sfx)
						}
					}
				}
			}
		}
		for sys := range sysBands {
			sort.Strings(sysBands[sys])
		}

		// PREPASS: count rows to print after applying skip rule
		printCount := 0
		for _, prns := range sysPRNs {
			// obtain its slice length for indexing
			for j := range prns {
				// Skip this row if all VS* at index j are zero
				if isAllVSZero(content, j) {
					continue
				}
				// also skip rows where PRN is zero (no satellite)
				if prns[j] <= 0 {
					continue
				}
				printCount++
			}
		}

		// write epoch header with final count
		epochLine := fmt.Sprintf("%s %d", formatObsEpoch(ts), printCount)
		if _, err := fmt.Fprintln(w, epochLine); err != nil {
			return err
		}

		// Now write per-satellite rows (skipping as required)
		// ### NOTICE: The dataset swaps the column names for the Doppler and Carrier Phase (likely a mistake), ###
		// ### so instead of the expected CLDS output, we manually use CDLS to ensure RINEX-compliant output:   ###
		measPrefixes := []string{"prMes_", "doMes_", "cpMes_", "cn0_"}
		for sys, prns := range sysPRNs {
			vsKey := sysToVS[sys]
			vsSlice, _ := asSliceAny(content[vsKey])

			for j, prn := range prns {
				// Skip rows where all systems’ VS at j are zero
				if isAllVSZero(content, j) {
					continue
				}
				// Also skip if this PRN is not valid for the current system
				if prn <= 0 || j >= len(vsSlice) {
					continue
				}

				var b strings.Builder
				fmt.Fprintf(&b, "%s", satID(sys, prn))

				for _, band := range sysBands[sys] {
					for _, pref := range measPrefixes {
						key := pref + band
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

	header, err := getHeaderDynamic()
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
