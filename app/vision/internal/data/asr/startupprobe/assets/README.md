# Vision startup probe audio

`vision-startup-check.wav` is a synthetic, non-user fixture that says
“铁虎会议助手启动检测”. It is PCM S16LE, 16 kHz, mono and is embedded into the
Vision binary. Replacing it requires keeping those audio constraints and
updating the startup-probe regression test.
