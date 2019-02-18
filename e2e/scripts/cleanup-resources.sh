#!/usr/bin/env bash

# Copyright 2017 DigitalOcean
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#    http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

### This script cleans up left-over resources (droplets, volumes, etc.) used
### on the DigitalOcean cloud.
### It requires an identifier to be passed in that serves to look up the
### resources. Ideally, this should be a tag, and less ideally, something that
### can be found in the name. Regardless, the script assumes the same
### identifier can be found across all resources, or otherwise the resource
### must be cleaned up manually.

set -o errexit
set -o pipefail
set -o nounset

s3base() {
  s3cmd --host "${SPACES_URL}" --host-bucket '%(bucket)s.'"${SPACES_URL}" --access_key "${S3_ACCESS_KEY_ID}" --secret_key "${S3_SECRET_ACCESS_KEY}" $*
}

: "${S3_ACCESS_KEY_ID:?must be defined}"
: "${S3_SECRET_ACCESS_KEY:?must be defined}"
: "${KOPS_REGION:?must be defined}"
readonly SPACES_URL="${KOPS_REGION}.digitaloceanspaces.com"

if ! type doctl > /dev/null 2>&1; then
  echo "doctl must be installed" >&2
  exit 1
fi

if ! type s3cmd > /dev/null 2>&1; then
  echo "s3cmd must be installed" >&2
  exit 1
fi

if [[ $# -ne 1 || $1 = "-h" ]]; then
  echo "usage: $(basename "$0") <identifier>" >&2
  exit 1
fi

readonly ID="$1"

echo 'deleting droplets'
# shellcheck disable=SC2207
droplets=( $(doctl compute droplet list --format ID,Tags | grep "${ID}" | awk '{print $1}' || true) )
readonly droplets
if [[ ${#droplets[@]} -gt 0 ]]; then
  doctl compute droplet delete --force "${droplets[@]}"
fi

echo 'deleting volumes'
volumes="$(doctl compute volume list --format ID,Name | grep "${ID}" | awk '{print $1}' || true)"
readonly volumes
for volume in ${volumes}; do
  doctl compute volume delete --force "${volume}"
done

echo 'deleting spaces'
spaces="$(s3base ls | grep "${ID}" | awk -F '//' '{print $2}' || true)"
readonly spaces
for space in ${spaces}; do
  s3base rb --quiet --recursive "s3://${space}"
done

num_lbs="$(doctl compute load-balancer list | tail -n +2 | wc -l)"
readonly num_lbs
if [[ ${num_lbs} -gt 0 ]]; then
  echo 'load-balancers cannot be deleted automatically; please remove from the following list manually where needed:'
  doctl compute load-balancer list
else
  echo 'no load-balancers must be deleted.'
fi
