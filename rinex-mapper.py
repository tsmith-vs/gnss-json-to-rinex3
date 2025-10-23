import georinex as gr
import numpy as np
import pandas as pd
import matplotlib.pyplot as plt
import matplotlib.dates as mdates
import warnings

warnings.filterwarnings('ignore', category=FutureWarning)

if len(sys.argv) != 2:
    print("Please provide a filename:")
    print("> python3 rinex-mapper.py observation12.obs")
    sys.exit()

input_file = sys.argv[1]

try:
    h = gr.rinexheader(input_file)
    print("Header parsed OK:", h.get('RINEX VERSION / TYPE', None))
except Exception as e:
    print("georinex.rinexheader() error:", repr(e))
# load with georinex
data = gr.load(input_file)

# build dict: satellite -> 1D numpy array of Doppler (D1C)
#dopplers = {
#    str(sv): np.asarray(data["D1C"].sel(sv=sv).values).ravel()
#    for sv in data["D1C"].sv.values
#}

"""
dopplers = {}
for ob in ["D1C", "D1X", "D1I"]:
    for sv in data["D1C"].sv.values:
        if ob == "D1C" and sv.startswith("G"):
            dopplers[str(sv)] = np.asarray(data[ob].sel(sv=sv).values).ravel()
        elif ob == "D1C" and sv.startswith("R"):
            dopplers[str(sv)] = np.asarray(data[ob].sel(sv=sv).values).ravel()
        elif ob == "D1X" and sv.startswith("E"):
            dopplers[str(sv)] = np.asarray(data[ob].sel(sv=sv).values).ravel()
        elif ob == "D1I" and sv.startswith("C"):
            dopplers[str(sv)] = np.asarray(data[ob].sel(sv=sv).values).ravel()
        elif ob == "D1I" and sv.startswith("J"):
            dopplers[str(sv)] = np.asarray(data[ob].sel(sv=sv).values).ravel()

"""
dopplers = {}
for ob in ["D1C", "D1X", "D2X"]:
    for sv in data["D1C"].sv.values:
        if ob == "D1C" and sv.startswith("G"):
            dopplers[str(sv)] = np.asarray(data[ob].sel(sv=sv).values).ravel()
        elif ob == "D1C" and sv.startswith("R"):
            dopplers[str(sv)] = np.asarray(data[ob].sel(sv=sv).values).ravel()
        elif ob == "D1X" and sv.startswith("E"):
            dopplers[str(sv)] = np.asarray(data[ob].sel(sv=sv).values).ravel()
        elif ob == "D2X" and sv.startswith("C"):
            dopplers[str(sv)] = np.asarray(data[ob].sel(sv=sv).values).ravel()
        elif ob == "D1C" and sv.startswith("J"):
            dopplers[str(sv)] = np.asarray(data[ob].sel(sv=sv).values).ravel()


# timestamps from the file (numpy datetime64) -> pandas DatetimeIndex
times = pd.to_datetime(data.time.values)

# create figure
fig, ax = plt.subplots(figsize=(12, 7))

# color cycle automatically used by matplotlib
for sv, arr in dopplers.items():
    y = np.asarray(arr).ravel()
    n = min(len(y), len(times))
    if n == 0:
        continue
    x = times[:n]
    y = y[:n]

    # skip satellites with only NaNs
    if np.all(np.isnan(y)):
        continue

    for i, y_value in enumerate(y):
        if y_value == 0:
            y[i] = np.nan


    # plot as line (no markers to keep svg/pdf clean). alpha for readability.
    ax.plot(x, y, linewidth=0.9, alpha=0.9, label=sv)

# format x-axis with readable datetime
ax.xaxis.set_major_formatter(mdates.DateFormatter("%Y-%m-%d\n%H:%M:%S"))
ax.xaxis.set_major_locator(mdates.AutoDateLocator())

ax.set_xlabel("Timestamp")
ax.set_ylabel("Doppler (Hz)")
ax.set_title(f"Doppler vs Time — all satellites from {input_file}")
ax.grid(True, linestyle=":", alpha=0.5)

# place legend outside to the right (adjust ncol if many sats)
ax.legend(loc="upper left", bbox_to_anchor=(1.02, 1.0), fontsize="small", ncol=1)

plt.tight_layout(rect=(0, 0, 0.78, 1.0))  # leave space on the right for the legend

# save as vector PDF (zoomable)
out_pdf = "dopplers_all.pdf"
plt.savefig(out_pdf, format="pdf", bbox_inches="tight")
plt.show()
plt.close(fig)

print(f"Saved plot to {out_pdf}")
 