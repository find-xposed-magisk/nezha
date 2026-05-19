import os
import time
import requests
from github import Github

MAX_RETRIES = 5
REQUEST_TIMEOUT = 120
# Gitee can need more than five minutes to write a 30 MB dashboard ZIP.
# Keep per-file retries low and let the workflow timeout bound worst-case hangs.
UPLOAD_TIMEOUT = 1800
UPLOAD_MAX_RETRIES = 2


def request_with_retries(client, method, url, retry_statuses=(429, 500, 502, 503, 504), **kwargs):
    kwargs.setdefault('timeout', REQUEST_TIMEOUT)
    response = None
    last_error = None

    for attempt in range(1, MAX_RETRIES + 1):
        try:
            response = client.request(method, url, **kwargs)
            if response.status_code not in retry_statuses:
                return response
            print(f"{method} {url} returned {response.status_code}, retrying... (attempt {attempt})")
        except requests.exceptions.RequestException as err:
            last_error = err
            print(f"{method} {url} failed: {err}, retrying... (attempt {attempt})")

        if attempt < MAX_RETRIES:
            time.sleep(min(60, attempt * 10))

    if last_error is not None:
        raise last_error
    return response


def get_github_latest_release():
    g = Github()
    repo = g.get_repo("nezhahq/nezha")
    release = repo.get_latest_release()
    if release:
        print(f"Latest release tag is: {release.tag_name}")
        print(f"Latest release info is: {release.body}")
        files = []
        for asset in release.get_assets():
            url = asset.browser_download_url
            name = asset.name

            response = requests.get(url, timeout=REQUEST_TIMEOUT)
            if response.status_code == 200:
                with open(name, 'wb') as f:
                    f.write(response.content)
                print(f"Downloaded {name}")
            else:
                print(f"Failed to download {name}")
            file_abs_path = get_abs_path(asset.name)
            files.append(file_abs_path)
        sync_to_gitee(release.tag_name, release.body, files)
    else:
        print("No releases found.")


def delete_gitee_releases(latest_id, client, uri, token):
    get_data = {
        'access_token': token
    }

    release_info = []
    release_response = request_with_retries(client, 'GET', uri, params=get_data)
    if release_response.status_code == 200:
        release_info = release_response.json()
    else:
        print(
            f"Request failed with status code {release_response.status_code}")

    release_ids = []
    for block in release_info:
        if 'id' in block:
            release_ids.append(block['id'])

    print(f'Current release ids: {release_ids}')
    if latest_id in release_ids:
        release_ids.remove(latest_id)
    else:
        print(f'Release #{latest_id} is not in the release list, skip deleting older releases.')

    for id in release_ids:
        release_uri = f"{uri}/{id}"
        delete_data = {
            'access_token': token
        }
        delete_response = request_with_retries(client, 'DELETE', release_uri, params=delete_data)
        if delete_response.status_code == 204:
            print(f'Successfully deleted release #{id}.')
        else:
            raise ValueError(
                f"Request failed with status code {delete_response.status_code}")


def sync_to_gitee(tag: str, body: str, files: slice):
    owner = "naibahq"
    repo = "nezha"
    release_api_uri = f"https://gitee.com/api/v5/repos/{owner}/{repo}/releases"
    api_client = requests.Session()
    api_client.headers.update({
        'Accept': 'application/json',
    })

    access_token = os.environ['GITEE_TOKEN']
    release_data = {
        'access_token': access_token,
        'tag_name': tag,
        'name': tag,
        'body': body,
        'prerelease': False,
        'target_commitish': 'master'
    }
    release_info = get_existing_gitee_release(api_client, release_api_uri, tag, access_token)
    if release_info is None:
        release_api_response = request_with_retries(api_client, 'POST', release_api_uri, json=release_data)
        if release_api_response.status_code == 201:
            release_info = release_api_response.json()
        else:
            print(f"Create release failed with status code {release_api_response.status_code}: {release_api_response.text}")
            release_info = get_existing_gitee_release(api_client, release_api_uri, tag, access_token)
            if release_info is None:
                raise ValueError(f"Unable to create or find Gitee release for {tag}")

    release_id = release_info.get('id')

    print(f"Gitee release id: {release_id}")
    asset_api_uri = f"{release_api_uri}/{release_id}/attach_files"
    uploaded_assets = get_gitee_asset_names(release_info)

    for file_path in files:
        asset_name = os.path.basename(file_path)
        if asset_name in uploaded_assets:
            print(f"{asset_name} already exists on Gitee, skip upload.")
            continue

        upload_gitee_asset(api_client, asset_api_uri, release_api_uri, access_token, tag, file_path)
        uploaded_assets.add(asset_name)

    # 仅保留最新 Release 以防超出 Gitee 仓库配额
    try:
        delete_gitee_releases(release_id, api_client, release_api_uri, access_token)
    except ValueError as e:
        print(e)

    api_client.close()
    print("Sync is completed!")


def get_abs_path(path: str):
    wd = os.getcwd()
    return os.path.join(wd, path)


def get_existing_gitee_release(client, release_api_uri, tag, token):
    response = request_with_retries(
        client,
        'GET',
        f"{release_api_uri}/tags/{tag}",
        params={'access_token': token},
    )
    if response.status_code == 200:
        return response.json()
    if response.status_code != 404:
        print(f"Get release by tag returned {response.status_code}: {response.text}")
    return None


def get_gitee_asset_names(release_info):
    return {asset.get('name') for asset in release_info.get('assets', []) if asset.get('name')}


def upload_gitee_asset(client, asset_api_uri, release_api_uri, token, tag, file_path):
    asset_name = os.path.basename(file_path)

    for attempt in range(1, UPLOAD_MAX_RETRIES + 1):
        try:
            with open(file_path, 'rb') as asset_file:
                response = client.post(
                    asset_api_uri,
                    params={'access_token': token},
                    files={'file': asset_file},
                    timeout=UPLOAD_TIMEOUT,
                )
            if response.status_code == 201:
                asset_info = response.json()
                print(f"Successfully uploaded {asset_info.get('name')}!")
                return
            print(f"Upload {asset_name} failed with status code {response.status_code}: {response.text}")
        except requests.exceptions.RequestException as err:
            print(f"Upload {asset_name} failed: {err}")

        release_info = get_existing_gitee_release(client, release_api_uri, tag, token)
        if release_info is not None and asset_name in get_gitee_asset_names(release_info):
            print(f"{asset_name} exists on Gitee after a failed upload attempt, continue.")
            return

        if attempt < UPLOAD_MAX_RETRIES:
            time.sleep(min(60, attempt * 10))

    raise ValueError(f"Failed to upload {asset_name} after {UPLOAD_MAX_RETRIES} attempts")


get_github_latest_release()
