machbase-neo download stat command line tool

## Build

```sh
$ go build -o neo-count .
```

## Usage

- show download statistics with details of the last 10 release.

```sh
$ ./neo-count

Total 127 releases (cached)
v8.0.45      2025-02-26   28  9.3 darwin-amd64:  4  darwin-arm64:  2  linux-amd64: 12  linux-arm32:  1  linux-arm64:  1  windows-amd64:  8
v8.0.44      2025-02-19   33  4.7 darwin-amd64:  1  darwin-arm64:  2  linux-amd64:  7  linux-arm32:  3  linux-arm64:  4  windows-amd64: 16
v8.0.43      2025-02-17   10  3.3 darwin-amd64:  1  darwin-arm64:  1  linux-amd64:  2  linux-arm32:  1  linux-arm64:  1  windows-amd64:  4
v8.0.42      2025-02-11   20  3.3 darwin-amd64:  3  darwin-arm64:  2  linux-amd64:  4  linux-arm32:  1  linux-arm64:  2  windows-amd64:  8
v8.0.41      2025-01-25   41  2.3 darwin-amd64:  3  darwin-arm64:  4  linux-amd64: 15  linux-arm32:  3  linux-arm64:  4  windows-amd64: 12
v8.0.40      2025-01-22   22  5.5 darwin-amd64:  2  darwin-arm64:  3  linux-amd64:  4  linux-arm32:  2  linux-arm64:  4  windows-amd64:  7
v8.0.39      2025-01-10   69  5.8 darwin-amd64:  7  darwin-arm64:  9  linux-amd64:  7  linux-arm32:  4  linux-arm64:  7  windows-amd64: 35
v8.0.39-rc1  2024-12-30   24  2.0 darwin-amd64:  4  darwin-arm64:  5  linux-amd64:  4  linux-arm32:  4  linux-arm64:  3  windows-amd64:  4
v8.0.38      2024-11-28  154  4.8 darwin-amd64:  5  darwin-arm64: 14  linux-amd64: 50  linux-arm32: 14  linux-arm64: 17  windows-amd64: 54
v8.0.37      2024-11-26   45 22.5 darwin-amd64:  4  darwin-arm64:  6  linux-amd64: 11  linux-arm32:  5  linux-arm64:  8  windows-amd64: 11
Total Downloads: 7524 package: 7327 src.zip: 197
```

- fields
```
version   release-date total-download  avg-download/day downloads-for-each-platforms
```

- shows details for latest 20 releases.

```sh
$ neo-count -last 20
```

