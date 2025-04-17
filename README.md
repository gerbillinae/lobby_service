
Requires a `config.env` with the following definitions:
```.env
# The container will be tagged <IMAGE_NAMESPACE>/<IMAGE_NAME>:<IMAGE_ID> where
# <IMAGE_ID> is the hash id of the current git HEAD.
IMAGE_NAME=
IMAGE_NAMESPACE=

# ARCH controls the GOARCH used in `task package`
ARCH=amd64
LOCAL_URL="http://localhost:8080"

AWS_ACCOUNT_ID=
AWS_REGION=

# aws CONTAINER_REGISTRY has the format <AWS_ACCOUNT_ID>.dkr.ecr.<AWS_REGION>.amazonaws.com
CONTAINER_REGISTRY=

K8S_SERVICE_NAME=lobby-service
K8S_INGRESS_GROUP_NAME=alb-group

PATH_PREFIX=/lobby

# DEPLOYMENT_URL is used for `task deployment-test`
DEPLOYMENT_URL=
```
