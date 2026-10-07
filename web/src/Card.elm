module Card exposing (view)

import Html exposing (Html, div, h2)
import Ui


{-| A shared content card with a title and top-right icon actions.
-}
view : Html msg -> List (Html msg) -> List (Html msg) -> Html msg
view heading actions content =
    div [ Ui.panel ]
        [ div [ Ui.panelHeader ]
            [ h2 [ Ui.panelHeading ] [ heading ]
            , div [ Ui.panelActions ] actions
            ]
        , div [] content
        ]
