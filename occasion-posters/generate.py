import os, html, json

BASE = os.path.dirname(os.path.abspath(__file__))
tpl = open(os.path.join(BASE, 'template.html')).read()

EVENTS = [
    dict(num='01', title='Nocturne', society='The Nocturne Society',
         kicker='An evening of <span>rare blooms</span> &amp; slow music',
         date='Sat <em>14</em> Nov', venue='The Conservatory',
         addr='Royal Botanic Gardens · London', doors='20:00 — LATE',
         prog=[('20:00','Dinner in the <em>Orangery</em>'),
               ('21:30','A performance in <em>four movements</em>'),
               ('23:00','After-hours <em>garden</em>')],
         specimen='Papaver somniferum', cta='Reserve a table',
         dress='Black tie · 21+', photo='macro-01.jpg', rot='-7', filt='grayscale(1) sepia(.24) brightness(.74) contrast(1.3)', op='.9'),
    dict(num='02', title='Solstice', society='The Nocturne Society',
         kicker='A table under <span>the longest light</span>',
         date='Fri <em>19</em> Jun', venue='The Orangerie',
         addr='Jardin du Luxembourg · Paris', doors='19:30 — 01:00',
         prog=[('19:30','Aperitif at <em>dusk</em>'),
               ('20:45','Supper by <em>candlelight</em>'),
               ('23:00','Dancing in the <em>glasshouse</em>')],
         specimen='Lilium candidum', cta='Request an invitation',
         dress='Summer formal', photo='macro-10.jpg', rot='-4', filt='grayscale(1) sepia(.2) brightness(.7) contrast(1.32)', op='.86'),
    dict(num='03', title='Aurora', society='The Nocturne Society',
         kicker='Two families, <span>one long table</span>',
         date='Sat <em>12</em> Sep', venue='Villa Cimbrone',
         addr='Ravello · Amalfi Coast', doors='16:00 — LATE',
         prog=[('16:00','Vows in the <em>cloister</em>'),
               ('18:30','Dinner on the <em>terrace</em>'),
               ('23:30','First dance at <em>midnight</em>')],
         specimen='Rosa gallica', cta='RSVP by 01 Aug',
         dress='Black tie', photo='macro-08.jpg', rot='-5', filt='grayscale(1) sepia(.26) brightness(.72) contrast(1.28)', op='.88'),
    dict(num='04', title='Velvet', society='The Nocturne Society',
         kicker='Forty years, <span>one velvet night</span>',
         date='Thu <em>30</em> Jul', venue='The Regency',
         addr='Tribeca · New York', doors='21:00 — 03:00',
         prog=[('21:00','Cocktails in the <em>library</em>'),
               ('22:30','A set by the <em>house band</em>'),
               ('00:00','Late-night <em>vinyl</em>')],
         specimen='Dahlia pinnata', cta='Reserve a table',
         dress='Black tie optional', photo='macro-06.jpg', rot='-8', filt='grayscale(1) sepia(.22) brightness(.66) contrast(1.34)', op='.84'),
    dict(num='05', title='Ember', society='The Nocturne Society',
         kicker='Ten years of <span>fire &amp; quiet</span>',
         date='Sun <em>08</em> Feb', venue='The Foundry',
         addr='Kreuzberg · Berlin', doors='18:00 — LATE',
         prog=[('18:00','Fireside <em>aperitif</em>'),
               ('19:30','A supper of <em>five courses</em>'),
               ('22:30','Cellar room, <em>until late</em>')],
         specimen='Paeonia officinalis', cta='Request an invitation',
         dress='Warm formal', photo='macro-07.jpg', rot='-3', filt='grayscale(1) sepia(.3) brightness(.58) contrast(1.36)', op='.8'),
    dict(num='06', title='Halcyon', society='The Nocturne Society',
         kicker='The last <span>long summer</span>',
         date='Sat <em>23</em> May', venue='The Quadrangle',
         addr='Magdalen College · Oxford', doors='14:00 — 23:00',
         prog=[('14:00','Garden <em>reception</em>'),
               ('17:30','Dinner in the <em>hall</em>'),
               ('20:00','Punting at <em>golden hour</em>')],
         specimen='Papaver rhoeas', cta='RSVP by 01 May',
         dress='Black tie · 18+', photo='macro-02.jpg', rot='-6', filt='grayscale(1) sepia(.24) brightness(.62) contrast(1.38)', op='.8'),
    dict(num='07', title='Meridian', society='The Nocturne Society',
         kicker='A new <span>line of sight</span>',
         date='Wed <em>21</em> Oct', venue='The Lighthouse',
         addr='Cais do Sodré · Lisbon', doors='19:00 — 01:00',
         prog=[('19:00','Preview at <em>sunset</em>'),
               ('20:30','Talk in the <em>tower</em>'),
               ('22:00','Drinks on the <em>roof</em>')],
         specimen='Strelitzia reginae', cta='RSVP',
         dress='Cocktail', photo='macro-03.jpg', rot='-7', filt='grayscale(1) sepia(.18) brightness(.56) contrast(1.4)', op='.8'),
    dict(num='08', title='Reverie', society='The Nocturne Society',
         kicker='A night <span>out of time</span>',
         date='Thu <em>31</em> Dec', venue='Hotel des Arts',
         addr='Nyhavn · Copenhagen', doors='21:00 — 05:00',
         prog=[('21:00','Midnight <em>supper</em>'),
               ('23:00','The clockwork <em>ball</em>'),
               ('04:00','Breakfast at <em>dawn</em>')],
         specimen='Paphiopedilum', cta='Reserve a table',
         dress='Black tie', photo='macro-05.jpg', rot='-4', filt='grayscale(1) sepia(.2) brightness(.72) contrast(1.3)', op='.88'),
    dict(num='09', title='Obsidian', society='The Nocturne Society',
         kicker='Works in <span>light &amp; void</span>',
         date='Fri <em>06</em> Mar', venue='Kunsthalle',
         addr='Museumsquartier · Vienna', doors='18:00 — 22:00',
         prog=[('18:00','Private <em>view</em>'),
               ('19:00',"Artist's <em>talk</em>"),
               ('20:30','Catalogue <em>signing</em>')],
         specimen='Anthurium andreanum', cta='RSVP',
         dress='Gallery formal', photo='macro-04.jpg', rot='-8', filt='grayscale(1) sepia(.14) brightness(.7) contrast(1.34)', op='.86'),
    dict(num='10', title='Bloom', society='The Nocturne Society',
         kicker='A garden <span>after dark</span>',
         date='Sun <em>05</em> Apr', venue='The Glasshouse',
         addr='Hortus Botanicus · Amsterdam', doors='17:00 — 23:00',
         prog=[('17:00','Spring <em>preview</em>'),
               ('18:30','Supper among <em>the palms</em>'),
               ('21:00','Candlelit <em>finale</em>')],
         specimen='Magnolia grandiflora', cta='Donate a table',
         dress='Garden formal', photo='macro-09.jpg', rot='-5', filt='grayscale(1) sepia(.26) brightness(.7) contrast(1.3)', op='.88'),
]

YEAR = 'MMXXVI'
outdir = os.path.join(BASE, 'generated')
os.makedirs(outdir, exist_ok=True)

for e in EVENTS:
    t = e['title']
    cut = 3 if len(t) > 5 else 2
    title_html = f'{t[:cut]}<span class="thin">{t[cut:]}</span>'
    prog = '\n'.join(
        f'    <div class="prow"><div class="t">{tm}</div><div class="d">{d}</div></div>'
        for tm, d in e['prog'])
    values = {
        '{{SOCIETY}}': e['society'],
        '{{NUM}}': e['num'],
        '{{YEAR_R}}': YEAR,
        '{{KICKER}}': e['kicker'],
        '{{TITLE_HTML}}': title_html,
        '{{PROGRAMME}}': prog,
        '{{SPECIMEN}}': f"Fig. {e['num']} — {e['specimen']} / 8× magnification",
        '{{DATE_BIG}}': e['date'],
        '{{DOORS}}': e['doors'],
        '{{VENUE}}': e['venue'],
        '{{ADDR}}': e['addr'],
        '{{CTA}}': e['cta'],
        '{{DRESS}}': e['dress'],
        '{{HANDLE}}': '@nocturnesociety',
        '{{PHOTO}}': '../assets/' + e['photo'],
        '{{FILTER}}': e['filt'],
        '{{OPACITY}}': e['op'],
        '{{ROT}}': e['rot'],
    }
    doc = tpl
    for k, v in values.items():
        doc = doc.replace(k, v)
    assert '{{' not in doc, [l for l in doc.splitlines() if '{{' in l][:3]
    open(os.path.join(outdir, f"poster-{e['num']}.html"), 'w').write(doc)

json.dump(EVENTS, open(os.path.join(BASE, 'events.json'), 'w'), indent=2)
print('generated', len(EVENTS))
