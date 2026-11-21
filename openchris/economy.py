"""Public releases and dates. A date in the calendar does not prove publication."""

import re
from datetime import date, datetime, timedelta
from html import unescape
from urllib.parse import urlsplit

import requests

IBGE = "https://servicodados.ibge.gov.br/api/v3/"
IBGE_PAGE = "https://www.ibge.gov.br/calendario-de-divulgacoes.html"
COPOM = "https://www.bcb.gov.br/api/servico/sitebcb/copom/"
BC_PAGE = "https://www.bcb.gov.br/controleinflacao/copom"

PRODUCTS = {9171: "Desemprego (PNAD Contínua)", 9227: "Vendas no varejo",
            9229: "Volume de serviços", 9256: "Inflação (IPCA)", 9260: "Prévia da inflação (IPCA-15)",
            9294: "Produção industrial", 9300: "Produto Interno Bruto (PIB)"}

# Second sessions listed in Comunicados 43.383 and 45.452. Extend on publication.
COPOM_CALENDARS = {
    2026: (43383, ("01-28", "03-18", "04-29", "06-17", "08-05", "09-16", "11-04", "12-09")),
    2027: (45452, ("01-27", "03-17", "04-28", "06-16", "08-04", "09-22", "10-27", "12-08")),
}


def official_json(url, **params):
    response = requests.get(url, params=params, timeout=(5, 20))
    response.raise_for_status()
    return response.json()


def plain(text):
    return " ".join(unescape(re.sub(r"<[^>]*>", " ", text)).split())


def ibge_date(value):
    return datetime.strptime(value[:10], "%d/%m/%Y").date()


def ibge_events(today):
    start, end = today - timedelta(days=45), today + timedelta(days=45)
    releases = {}
    for page in range(1, 11):
        data = official_json(IBGE + "noticias/", tipo="release", qtd=80, page=page)
        for item in data["items"]:
            published = ibge_date(item["data_publicacao"])
            url = urlsplit(item["link"])
            if not start <= published <= today or url.hostname != "agenciadenoticias.ibge.gov.br":
                continue
            for product in re.findall(r"\d+", str(item.get("produtos_relacionados") or item.get("produto_id", ""))):
                releases[(int(product), published)] = (
                    plain(item["titulo"]), url._replace(scheme="https").geturl())
        if not data["nextPage"] or ibge_date(data["items"][-1]["data_publicacao"]) < start:
            break
    result = []
    for page in range(1, 21):
        data = official_json(IBGE + "calendario/", qtd=150, page=page)
        for item in data["items"]:
            due = ibge_date(item["data_divulgacao"])
            product = int(item["produto_id"])
            if product in PRODUCTS and start <= due <= end:
                fact, url = releases.get((product, due), (None, IBGE_PAGE))
                result.append((f"ibge:{item['id']}", due, PRODUCTS[product], fact, url))
        if not data["nextPage"] or ibge_date(data["items"][-1]["data_divulgacao"]) < start:
            break
    if not result:
        raise ValueError("Nenhum indicador encontrado no calendário do IBGE.")
    return result


def copom_events(today):
    start, end = today - timedelta(days=60), today + timedelta(days=60)
    meetings = {}
    for year, (number, dates) in COPOM_CALENDARS.items():
        source = f"https://www.bcb.gov.br/estabilidadefinanceira/exibenormativo?tipo=Comunicado&numero={number}"
        for month_day in dates:
            due = date.fromisoformat(f"{year}-{month_day}")
            meetings[due] = (None, source)
    data = official_json(COPOM + "comunicados", quantidade=6)["conteudo"]
    if not data:
        raise ValueError("O Banco Central não retornou os comunicados do Copom.")
    for item in data:
        due = date.fromisoformat(item["dataReferencia"][:10])
        source = COPOM + f"comunicados_detalhes?nro_reuniao={int(item['nro_reuniao'])}"
        meetings[due] = (plain(item["titulo"]), source)
    return [(f"copom:{due}", due, "Decisão do Copom sobre a Selic", fact, url)
            for due, (fact, url) in meetings.items() if start <= due <= end]


FEEDS = {"IBGE": (IBGE_PAGE, ibge_events), "Banco Central": (BC_PAGE, copom_events)}
