# gourlwatcher
Demon which monitors URLs and notify creator about changes

## Install
go get -u github.com/ad/gourlwatcher

## Run
nohup gourlwatcher -token telegram:token -secret auth_secret &

## Commands
/auth secret

/info url_id

/diff url_id

/delete url_id

/toggleenabled url_id

/togglecontains url_id

/togglediff url_id

/add url

check string in result body


/updatesearch url_id

check string in result body
