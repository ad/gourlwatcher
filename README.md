# gourlwatcher
gourlwatcher monitors URLs and notify creator about changes by telegram

## Install
go get -u github.com/ad/gourlwatcher

## Run
nohup gourlwatcher -token telegram:token -secret auth_secret &

## Commands
/auth secret

/list

/info url_id

/delete url_id

/toggleenabled url_id

/togglecontains url_id

/togglerecovered url_id

/shot url_id

/add url

check string in result body


/updateurl url_id

new url


/updatesearch url_id

check string in result body


/updatetitle url_id

new title
