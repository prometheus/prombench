#!/bin/sh

printf "READY\n"; # required for Header information by supervisord

while read line; do
  echo "Processing Event: $line" >&2;
  killall -SIGINT supervisord
done < /dev/stdin