News banners
============

Put images here (.jpg .jpeg .png .gif) and reference them in data\news.json:

    data\news\banner1.jpg   ->   "NewsIcon": "/news/banner1.jpg"

The game puts NewsIcon right after the server address, so the server adds
the leading "/" when it is missing ("news/banner1.jpg" works too).

default.jpg (or default.png) is shown for news items with "NewsIcon": "".

The game can only decode JPEG, so the server converts every banner to a
baseline RGB JPEG before sending it (transparency becomes black, images
larger than 1024x1024 are scaled down). Files that cannot be read are not
sent. Any size works; the game scales the banner.
