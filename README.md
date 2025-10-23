# GNSS JSON to RINEX3 Converter

This script is specially designed to convert the JSON data from the **1221/Processed Data/** folder of the **GNSS Dataset (with Interference and Spoofing) Part III** found at the link below to RINEX v3.04:

https://data.mendeley.com/datasets/nxk9r22wd6/3

### Usage
> Tested on WSL-Ubuntu-24.04
1. Download this repository
2. Build the go program with:
    
    `go build convert_to_rinex.go`
3. The script is designed to convert a single observation file provided to stdin:
    
    `./convert_to_rinex observation12.json`
    
    To convert multiple files at a time, use a for loop:
    
    `for file in $(ls data);do ./convert_to_rinex data/$file; done`
    
    ^ This loop will list each file present in a /data/ directory and run the script on each file
4. The output RINEX files will be in the ./rinex/ directory created by the script

### Rinex Reader
The rinex-reader.py file is a simple Python script for validating the RINEX observation files. By default, the script only validates the header and epochs for time's sake. If you want to also validate the entire RINEX body, simply uncomment the code in the Python script at "3️⃣ Read the data structure".

##### Usage
1. `pip install georinex xarray yaspin`
2. For a single file:

    `python3 rinex-reader.py ./rinex/observation12.obs`

    To convert multiple files:

    `for file in $(ls rinex);do python3 rinex-reader.py rinex/$file; done`

    ^ This loop will parse each file present in the /rinex/ directory
3. The output shows basic header and epochs info

### Rinex Mapper
The rinex-mapper.py file is used for visualizing the doppler data from a RINEX .obs file.

##### Usage
1. `pip install georinex matplotlib numpy pandas`
2. For a single file:

    `python3 rinex-mapper.py ./rinex/observation12.obs`

3. The output is a matplotlib graph of the data


> @tsmith-vs