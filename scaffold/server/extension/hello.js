// Example fylr API extension of __PLUGIN_NAME__: reads the request from
// stdin, answers with JSON on stdout.

let chunks = []
process.stdin.on("data", (d) => chunks.push(d))
process.stdin.on("end", () => {
    process.stdout.write(JSON.stringify({
        plugin: "__PLUGIN_NAME__",
        hello: "world",
    }))
})
