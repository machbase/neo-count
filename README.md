# neo-count

GitHub 릴리스 에셋의 다운로드 수를 조회한다. Go 1.26 이상이 필요하다.

## 사용법

최근 10개 릴리스의 누적 다운로드 수, 릴리스 간 기간을 사용한 평균 및 플랫폼별 누적 수를 조회한다.

```sh
go run . -last 10
```

최근 10개 릴리스의 최근 30일간 일별 다운로드 근사치를 조회한다.

```sh
go run . -daily -last 10
```

일별 조회는 데이터 브랜치를 자동으로 내려받고 갱신한다. 저장소가 공개되어 있고 첫 수집으로 데이터 브랜치가 생성되어 있어야 한다.

누적 출력의 `src.zip`은 이름 규칙상 패키지가 아닌 릴리스 에셋을 뜻하며 GitHub 자동 생성 소스 아카이브 다운로드 수가 아니다.

기본 데이터 디렉터리는 `~/.config/neo-count`다. `-data-dir`로 캐시와 스냅샷의 공통 상위 경로를 변경할 수 있다.

```text
~/.config/neo-count/
  cache/releases.json            # 누적 통계 API 캐시
  download-stats/snapshots/      # 데이터 브랜치 체크아웃의 스냅샷
```

기존 `~/.config/neo-download-count` 캐시는 더 이상 사용하지 않으며 삭제하지 않는다. 다음 누적 통계 조회 시 새 경로에 캐시를 생성한다.

## 일별 다운로드 근사치

일별 통계의 "하루"는 **한국 표준시(KST, UTC+9)** 기준이다. 기본 브랜치의 GitHub Actions가 매일 00:07, 00:17, 00:27 KST(15:07, 15:17, 15:27 UTC)에 `machbase/neo-server`의 공개 릴리스와 모든 에셋을 수집한다. GitHub 스케줄은 지연되거나 누락될 수 있어 00:00~00:30 KST 창 안에 여러 번 예약하며, 창 안의 측정 중 마지막 것이 저장된다. 각 수집의 누적값은 `download-stats` 브랜치의 `snapshots/YYYY-MM-DD.json`(KST 날짜)에 저장된다. 설정에서 Actions를 활성화하고 `GITHUB_TOKEN`에 저장소 콘텐츠 쓰기를 허용해야 한다. GitHub가 자동 발급하는 `GITHUB_TOKEN`을 checkout이 Git 인증에 사용하므로 별도의 개인 액세스 토큰이나 저장소 시크릿은 필요하지 않다. 수집 프로그램에도 같은 토큰을 전달해 GitHub API 인증에 사용한다. 브랜치 보호 규칙이 있다면 해당 브랜치에 대한 Actions의 push를 허용해야 한다. 브랜치는 첫 정상 수집 시 생성된다.

일별 조회는 `go-git`으로 데이터 브랜치를 최초 clone하고 필요할 때 갱신하므로 별도 Git 설치나 체크아웃이 필요 없다. 해당 KST 일자의 스냅샷을 00:35 KST 이후 정상 확인했다면 다음 날 00:07 전까지 로컬 데이터를 재사용한다. 00:07부터 00:35까지는 조회 시 최소 5분 간격으로 확인하며, 00:35 이후 한 번 더 확인해 수집 결과의 게시 지연을 고려한다. 해당 날짜 스냅샷이 없으면 이후에도 조회 시 최소 5분 간격으로 재확인한다. 실패한 확인은 성공으로 기록하지 않는다.

00:35 이후 게시된 늦은 변경은 다음 예정 확인까지 자동 반영되지 않을 수 있다. 즉시 갱신하려면 `go run . -daily -refresh`를 사용한다. 네트워크 오류, 브랜치가 아직 없거나 기존 체크아웃에 로컬 변경이 있는 경우 오류를 표시하고 기존 파일을 덮어쓰지 않는다. 기존 체크아웃은 예상 저장소의 `download-stats` 브랜치여야 한다.

네트워크에 연결하지 않고 이미 내려받은 데이터를 조회하려면 다음 명령을 사용한다. `-offline`과 `-refresh`는 함께 사용할 수 없다.

```sh
go run . -daily -offline -last 10 -days 30
```

`-data-dir`을 지정하면 해당 경로의 `download-stats` 하위에서 데이터를 관리한다. 동시 접근을 막기 위해 데이터 디렉터리에 잠금 파일을 사용한다. 비정상 종료 후 잠금 파일이 남았다면 다른 수집·조회 프로세스가 실행 중이지 않은지 확인한 뒤 `.download-stats.lock`을 제거한다.

`-last`는 최근 릴리스 수, `-days`는 최근 완료 KST 날짜 수다. 조회 종료 시점은 마지막 정상 스냅샷이며 데이터가 오래되면 경고한다. 수집은 `-last`와 무관하게 모든 공개 릴리스를 대상으로 한다.

날짜별 값은 해당 날짜 시작과 다음 날 시작 부근의 마지막 정상 스냅샷 차이로, **정확한 달력일 집계가 아닌 근사치**다. 표의 관측 구간은 각 수집의 종료 시각이다. API 페이지는 순차적으로 조회하므로 각 에셋의 실제 관측 시각도 그 수집 시작과 종료 사이에 있다. 자동 생성 소스 아카이브는 포함하지 않으며, 일별 합계는 기존 출력의 패키지 에셋과 같은 범위다.

일별 표는 큰 다운로드 수에 천 단위 구분 기호를 사용하고, 셀에는 짧은 `N/A`를 표시한다. 원인을 표 아래에 한 번만 요약하며, 누락 스냅샷 경고도 날짜별 반복 대신 개수와 범위로 표시한다. 관측 구간은 `MM-DD HH:MM to MM-DD HH:MM` 형식이다. `—`는 해당 날짜 이후 게시된 릴리스, `0`은 관측된 증가 없음이다.

수집이 지연되어도 같은 KST 날짜 안에 완료되면 스냅샷을 저장한다. 다만 00:30 KST 이후에 완료된 스냅샷이 시작 또는 종료 경계인 날짜에는 잘못된 일별 수치를 표시하지 않고 `N/A (late snapshot)`을 표시한다. 같은 날짜에 여러 번 수집하면 00:30 이전 측정 중 마지막 것을 우선하며, 그런 측정이 없으면 가장 이른 늦은 측정을 보관한다. KST 자정을 넘겨 완료된 수집은 저장하지 않는다.

실패, 누락, 손상된 스냅샷, 삭제·교체된 에셋 또는 누적값 감소 등으로 차이를 신뢰할 수 없는 경우에도 `N/A`와 이유를 표시한다. 릴리스가 아직 게시되지 않은 날짜는 `—`, 측정된 증가가 없는 경우는 `0`이다. 수집 전에 지나간 날짜나 누락된 날짜는 복원할 수 없다. 수동으로 워크플로를 재실행해 저장된 경계가 바뀌면 인접 날짜의 값도 달라질 수 있다.

워크플로 실패 및 마지막 성공 시각을 확인해야 한다. GitHub Actions 스케줄은 지연·누락될 수 있으며 공개 저장소의 장기간 비활동 시 비활성화될 수 있다. 스냅샷은 우선 무기한 보관한다.

## 빌드 및 누적 출력 예시

```sh
go build -o neo-count .
```

아래는 최근 10개 릴리스의 누적 통계 출력 예시다. 수치는 조회 시점에 따라 달라진다.

```text
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

출력 필드:

```text
version   release-date total-download  avg-download/day downloads-for-each-platforms
```

최근 20개 릴리스를 조회하려면:

```sh
./neo-count -last 20
```
