module Card exposing (view, viewWithAttributes, viewWithSubtitle)

import Html exposing (Html, div, h2, p)
import Ui


{-| A shared content card with a title and top-right icon actions.
-}
view : Html msg -> List (Html msg) -> List (Html msg) -> Html msg
view heading actions content =
    viewWithSubtitle heading Nothing actions content


viewWithSubtitle : Html msg -> Maybe (Html msg) -> List (Html msg) -> List (Html msg) -> Html msg
viewWithSubtitle heading subtitle actions content =
    viewWithAttributes [] heading subtitle actions content


viewWithAttributes : List (Html.Attribute msg) -> Html msg -> Maybe (Html msg) -> List (Html msg) -> List (Html msg) -> Html msg
viewWithAttributes attributes heading subtitle actions content =
    div (Ui.panel :: attributes)
        [ div [ Ui.panelHeader ]
            [ case subtitle of
                Just description ->
                    div []
                        [ h2 [ Ui.panelHeading ] [ heading ]
                        , p [ Ui.panelSubtitle ] [ description ]
                        ]

                Nothing ->
                    h2 [ Ui.panelHeading ] [ heading ]
            , div [ Ui.panelActions ] actions
            ]
        , div [] content
        ]
