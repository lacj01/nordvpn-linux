#!/usr/bin/env bash
set -euxo pipefail

declare -A LIB_NAME_TO_PACKAGE=(
    [libtelio]=github.com/NordSecurity/libtelio-go
    [libdrop]=github.com/NordSecurity/libdrop-go
)

declare -A LIB_NAME_TO_VERSION
[[ -n "${LIBTELIO_VERSION:-}" ]] && LIB_NAME_TO_VERSION[libtelio]=$LIBTELIO_VERSION
[[ -n "${LIBDROP_VERSION:-}" ]] && LIB_NAME_TO_VERSION[libdrop]=$LIBDROP_VERSION

declare -A LIB_NAME_TO_BINDINGS_VERSION
[[ -n "${LIBTELIO_BINDINGS_VERSION:-}" ]] && LIB_NAME_TO_BINDINGS_VERSION[libtelio]=$LIBTELIO_BINDINGS_VERSION
[[ -n "${LIBTELIO_BINDINGS_VERSION:-}" ]] && LIB_NAME_TO_BINDINGS_VERSION[libdrop]=$LIBTELIO_BINDINGS_VERSION

lib_name=$1
repo_path="${LIB_NAME_TO_PACKAGE[$lib_name]}"
lib_version="${LIB_NAME_TO_VERSION[$lib_name]:-}"
if [[ -z "${lib_version}" ]]; then
  return 0
fi

bindings_version="${LIB_NAME_TO_BINDINGS_VERSION[$lib_name]:-}"

major_version=$(echo "${lib_version}" | cut -d'.' -f1)

full_package_path=$repo_path/$major_version@$lib_version
if [[ -n "${bindings_version}" ]]; then
  full_package_path=$repo_path/$major_version@$bindings_version
fi

go get "$full_package_path"