set -eu
slotroot=/usr/bin/siglent/usr/media/U-disk0/agent-slots
staged=$slotroot/staging/panel-current-app
test -s "$staged"
for slot in A B emergency; do
    cp "$staged" "$slotroot/$slot/app.panel-new"
    chmod 755 "$slotroot/$slot/app.panel-new"
    cmp "$staged" "$slotroot/$slot/app.panel-new"
done
for slot in A B emergency; do
    mv "$slotroot/$slot/app.panel-new" "$slotroot/$slot/app"
    cmp "$staged" "$slotroot/$slot/app"
done
chmod 755 "$slotroot/staging/panel-loader"
sync
