import georinex as gr
import sys
import xarray as xr
import warnings
from yaspin import yaspin

warnings.filterwarnings('ignore', category=FutureWarning)

if len(sys.argv) != 2:
    print("Please provide a filename:")
    print("> python3 rinex-reader.py observation12.obs")
    sys.exit()

input_file = sys.argv[1]

# Fix xarray defaults before loading (for new xarray versions)
xr.set_options(use_new_combine_kwarg_defaults=False)

print(f"\n[###] {input_file}")
# --- 1️⃣ Parse header ---
try:
    h = gr.rinexheader(input_file)
    print(f"[#] Header parsed successfully: Version: {h.get('version', None)}, FileType: {h.get('filetype', None)}, RinexType: {h.get('rinextype', None)}")
except Exception as e:
    print("[-] Error parsing header:", repr(e))
    sys.exit()

# --- 2️⃣ Check RINEX time span ---
try:
    times = gr.gettime(input_file)
    print(f"[#] Time info extracted successfully.")
    print(f"Start time: {times[0]}")
    print(f"End time:   {times[-1]}")
    print(f"Total epochs: {len(times)}")
except Exception as e:
    print("[-] Error extracting time info:", repr(e))

# # --- 3️⃣ Read the data structure ---
# try:
#     with yaspin(text="Parsing body...", color="cyan") as spinner:
#         ds = gr.load(input_file)
#         spinner.ok("✔ Done!")
#         print("[#] Observation data structure loaded successfully.")
#         print(ds)
# except Exception as e:
#     print("[-] Error reading observation data:", repr(e))

print()
