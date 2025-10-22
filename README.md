# GNSS JSON to RINEX3 Converter

This script is specially designed to convert the JSON data from the 1221/Processed Data/ folder of the GNSS Dataset (with Interference and Spoofing) Part III found at the link below to RINEX v3.04:

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

@tsmith-vs