set -eu
slotroot=/usr/bin/siglent/usr/media/U-disk0/agent-slots
bundle=$slotroot/staging
test -s "$bundle/panel-sram-app"
test -s "$bundle/panel-sram-current.rbf"
test -s "$bundle/panel-sram-start"
chmod 755 "$bundle/panel-sram-app" "$bundle/panel-loader"
for slot in A B emergency; do
    cp "$bundle/panel-sram-start" "$slotroot/$slot/app.panel-new"
    chmod 755 "$slotroot/$slot/app.panel-new"
    cmp "$bundle/panel-sram-start" "$slotroot/$slot/app.panel-new"
done
for slot in A B emergency; do
    mv "$slotroot/$slot/app.panel-new" "$slotroot/$slot/app"
    cmp "$bundle/panel-sram-start" "$slotroot/$slot/app"
done
sync
