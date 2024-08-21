# Windows Registrator

Service registry bridge for Windows Docker.

##  consul demo

running Registrator-containerd looks like this:

```
./nerdctl  run --rm --hostname=192.168.102.84 --network=host -v /run/containerd/containerd.sock:/run/containerd/containerd.sock dockerhub.uc108.org/library/registrator-containerd:1.0.0 -internal=true -resync=240 -cleanup  consul://127.0.0.1:8500
```

## build env

```
docker run -d -it -v /home/docker/dockerfile/registrator-containerd:/go/src/github.com/king311247/registrator \
-e GOPROXY=https://goproxy.cn,direct -e GO111MODULE=on \
docker.m.daocloud.io/library/golang:1.21-alpine sh
```


## License

MIT

<img src="https://ga-beacon.appspot.com/UA-58928488-2/registrator/readme?pixel" />
