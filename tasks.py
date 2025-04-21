import os
import invoke
from jsonschema import validate, ValidationError
import json
import yaml

def success(msg):
    print(f"\u001B[0;32m{msg}\u001B[0m")

def info(msg):
    print(f"\u001B[0;34m{msg}\u001B[0m")

def warning(msg):
    print(f"\u001B[0;33m{msg}\u001B[0m")

def stage(msg):
    print(f"\u001B[0;36m{msg}\u001B[0m")

def root_dir():
    return os.path.dirname(os.path.abspath(__file__))

def validate_json_schema(data, schema_file):
    stage("Validating JSON schema")
    try:
        with open(schema_file, 'r') as f:
            schema = json.load(f)
        validate(instance=data, schema=schema)
    except json.JSONDecodeError as e:
        raise ValueError(f"Invalid JSON data: {e}")
    except ValidationError as e:
        raise ValueError(f"JSON data does not match schema: {e.message}")

@invoke.task
def _build(c : invoke.Context, goarch: str = "amd64", goos: str = "linux"):
    import shutil
    stage("Building application")
    c.run("GOARCH=amd64 GOOS=linux task --taskfile ./app/Taskfile.yml build")
    if os.path.exists("./build"):
        shutil.rmtree("./build")
    return c.run("mv ./app/build .").ok

@invoke.task
def _set_config(c : invoke.Context, config : {}):
    stage("Setting config")
    try:
        validate_json_schema(config, f"{root_dir()}/build/config.schema.json")
    except ValueError as e:
        print(f"Validation error: {e}")
        return False

    with open(f"{root_dir()}/build/config.json", 'w') as f:
        json.dump(config, f, indent=4)

    return True

@invoke.task
def _package(c : invoke.Context, image_namespace: str, image_name: str, image_version: str, arch: str = "amd64"):
    stage("Packaging application")
    image = f"{image_namespace}/{image_name}"
    exact_image = f"{image}:{image_version}"

    if not c.run(f"docker build --platform linux/{arch} -t {image}:latest -f ./Dockerfile ./build").ok:
        print("Docker build failed.")
        return False

    if not c.run(f"docker tag {image}:latest {exact_image}").ok:
        print("Docker tag failed.")
        return False

    return True

@invoke.task
def _test_package(c : invoke.Context, config_file : str, image_namespace: str, image_name: str, image_version: str, port: str):
    stage("Testing package")
    image = f"{image_namespace}/{image_name}"
    exact_image = f"{image}:{image_version}"

    print(f"Starting image: {exact_image}")
    if not c.run(f"docker run -d -p 8080:8080 {exact_image}").ok:
        print("Docker run failed.")
        return False

    try:
        if not c.run(f'python3 ./test/test.py --address "http://localhost:{port}" --app-config "{config_file}"').ok:
            print("Test failed.")
            return False
    finally:
        print("Stopping image")
        c.run(f"docker stop $(docker ps -q --filter ancestor={exact_image})")
        c.run(f"docker rm $(docker ps -aq --filter ancestor={exact_image})")

    return True

@invoke.task
def _push_container(c : invoke.Context, aws_account_id : str, aws_region: str, image_namespace: str, image_name: str, image_version: str):
    stage("Pushing container to ECR")
    image = f"{image_namespace}/{image_name}"
    exact_image = f"{image}:{image_version}"

    container_registry = f"{aws_account_id}.dkr.ecr.{aws_region}.amazonaws.com"
    c.run(f"aws ecr get-login-password --region {aws_region} | docker login --username AWS --password-stdin {container_registry}")

    c.run(f"aws ecr create-repository --repository-name {image} || true")

    res = c.run(f'aws ecr list-images --repository-name {image} | jq "[.imageIds[].imageTag]"')
    images = json.loads(res.stdout)
    if image_version in images:
        print(f"Image {image_version} already exists in ECR, skipping push.")
        return True

    c.run(f"docker tag {exact_image} {container_registry}/{exact_image}")
    c.run(f"docker push {container_registry}/{exact_image}")
    return True

@invoke.task
def _deploy(c : invoke.Context, aws_account_id: str, aws_region: str, config : {}, image_namespace: str, image_name: str, image_version: str, arch: str, test_port : str, output: str):
    c.cd(root_dir())

    if not _build(c, goarch=arch):
        print("Build failed. Exiting.")
        return False

    if not _set_config(c, config):
        print("Config file validation failed. Exiting.")
        return False

    if not _package(c, image_namespace, image_name, image_version, arch):
        print("Packaging failed. Exiting.")
        return False

    if not _test_package(c, "./build/config.json", image_namespace, image_name, image_version, test_port):
        print("Packaging failed. Exiting.")
        return False

    if not _push_container(c, aws_account_id, aws_region, image_namespace, image_name, image_version):
        print("Push failed. Exiting.")
        return False

    out = yaml.dump({
        "image_repository": f"{aws_account_id}.dkr.ecr.{aws_region}.amazonaws.com/{image_namespace}/{image_name}",
        "image_namespace": image_namespace,
        "image_name": image_name,
        "image_version": image_version,
    })
    with open(output, 'w') as f:
        f.write(out)

def hash_config(cfg):
    import hashlib
    config_str = json.dumps(cfg, sort_keys=True)
    return hashlib.sha256(config_str.encode()).hexdigest()[:7]

def githash():
    import subprocess
    res = subprocess.run(["git", "diff-index", "--quiet", "HEAD"], cwd=root_dir())
    if res.returncode != 0:
        raise ValueError("Uncommitted changes detected. Exiting.")

    try:
        return subprocess.check_output(['git', 'rev-parse', '--short', 'HEAD'], cwd=root_dir()).strip().decode('utf-8')
    except subprocess.CalledProcessError as e:
        raise ValueError("Error getting git hash.")

def image_version(cfg):
    return f"{githash()}-{hash_config(cfg)}"

@invoke.task
def deploy(c : invoke.Context, config : str, output : str):
    print("ROOT", root_dir())

    with open(config, 'r') as f:
        cfg = yaml.safe_load(f)

    try:
        version = image_version(cfg)
    except ValueError as e:
        print(f"{e}")
        return False

    with c.cd(root_dir()):
        _deploy(
            c,
            aws_account_id=cfg["aws_account_id"],
            aws_region=cfg["aws_region"],
            config=cfg["app_config"],
            image_namespace=cfg["image_namespace"],
            image_name=cfg["image_name"],
            image_version=version,
            arch=cfg["arch"],
            test_port=cfg["app_config"]["port"],
            output=output
        )

if __name__ == "__main__":
    deploy(
        invoke.Context(),
        "./config.yaml",
        "./build/manifest.yaml"
    )