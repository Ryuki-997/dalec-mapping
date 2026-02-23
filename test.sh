eval "$(go run . | grep '^specPaths=')"
echo "$specPaths"