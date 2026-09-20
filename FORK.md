# PUBG fork 운영 정책

이 저장소는 [`namecheap/terraform-provider-jenkins`](https://github.com/namecheap/terraform-provider-jenkins)를 upstream으로 추적하는 PUBG fork입니다. 이 문서는 fork에만 필요한 차이를 작게 유지하면서 upstream 변경을 정기적으로 반영하는 방법을 정의합니다.

## 작업 지침

- 작업을 시작하기 전에 현재 branch, 변경 파일, remote와 upstream 기준점을 확인합니다.
- 변경 목적을 upstream 동기화, 일반 기능 개선 또는 PUBG 전용 변경 중 하나로 분류합니다.
- Jenkins 일반 사용자에게도 유효한 수정은 먼저 upstream 기여를 검토합니다.
- PUBG 환경에만 필요한 수정은 fork에서 관리하되, upstream 코드의 불필요한 이름 변경이나 구조 변경은 피합니다.
- 기존 코드와 자동화가 요구 사항을 충족하면 수정하지 않습니다. 새 파일이나 추상화보다 upstream의 기존 방식을 우선합니다.
- `main`에는 pull request로만 반영하고, 직접 push하지 않습니다.
- 생성된 `docs/`는 직접 수정하지 않고, schema 또는 `templates/`를 수정한 뒤 `make generate`로 갱신합니다.
- 프로그램 코드와 Terraform 코드의 검증은 모든 변경을 마친 뒤 한 번에 실행합니다. 실행한 검증과 실행하지 못한 검증을 결과에 구분해서 기록합니다.
- push, release와 registry 배포는 해당 작업을 명시적으로 요청받았을 때만 진행합니다.

## Upstream 동기화

- 동기화는 별도 branch와 pull request로 진행합니다.
- 동기화를 시작하기 전에 upstream 변경 범위와 현재 fork 고유 변경을 비교합니다.
- 충돌은 upstream 내용을 우선하되, 여전히 필요한 fork 고유 변경만 유지합니다.
- upstream 커밋의 계보를 보존하기 위해 동기화 pull request는 squash 또는 rebase하지 않고 merge commit으로 병합합니다.
- 모든 충돌을 해결한 뒤 변경 범위에 맞는 검증을 한 번에 실행합니다.
- upstream tag는 참고용으로만 가져오며 fork에 그대로 게시하지 않습니다.

## Provider와 릴리스

현재 Go module path와 Terraform provider 주소는 upstream과 같은 `namecheap/jenkins`입니다. 따라서 이 fork는 아직 별도의 `pubg/jenkins` 배포판이 아닙니다.

`.github/workflows/versioning.yml`과 `.github/workflows/release.yml`은 GitHub의 `repository.fork` 값을 확인하고 fork에서는 릴리스 job을 실행하지 않습니다. 독립적인 PUBG 배포판이 필요해지면 별도 작업으로 provider 주소, 버전 정책, 서명 키와 배포 절차를 정의합니다.

현재 fork 고유 변경은 다음과 같습니다.

- `README.md`의 fork 안내와 fork 상태 badge
- `FORK.md`
- `.github/CODEOWNERS`의 fork 관리자
- 릴리스 job의 fork 확인 조건
- Codecov 대상 repository를 나타내는 `${{ github.repository }}` 값

## Branch 보호

`main`에서는 직접 push와 강제 push를 막고, 최소 한 명의 승인과 다음 상태 검사를 요구합니다.

- `CI OK`
- `Conventional PR Title`
- `CodeQL (go)`

`CI OK`가 lint, unit, acceptance, integration과 docs job의 결과를 모두 집계하므로, 개별 job을 필수 검사로 중복 등록할 필요는 없습니다.
